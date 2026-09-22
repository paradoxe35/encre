use super::audio::SAMPLE_RATE;

/// How far before a forced cut to look for a quiet moment.
const SEARCH_WINDOW: usize = 3 * SAMPLE_RATE as usize;
/// Loudness is judged over slices this long, stepped half a slice at a time.
const SLICE: usize = SAMPLE_RATE as usize / 50;

/// Speech kept from a take at 16 kHz, with the offsets where a pause split it into phrases.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Speech {
    pub samples: Vec<f32>,
    /// Ascending sample offsets at which a phrase starts after a pause.
    pub pauses: Vec<usize>,
}

impl Speech {
    pub fn is_empty(&self) -> bool {
        self.samples.is_empty()
    }

    pub fn take(&mut self) -> Speech {
        std::mem::take(self)
    }

    /// Appends a later burst, re-basing its pauses onto this one.
    pub fn append(&mut self, burst: Speech) {
        let offset = self.samples.len();
        self.pauses.extend(burst.pauses.into_iter().map(|p| p + offset));
        self.samples.extend(burst.samples);
    }

    /// Splits into pieces of at most `max` samples, cutting at pauses. Phrases are packed
    /// together while they fit; a single phrase longer than `max` is cut into near-equal
    /// parts, each cut at the quietest moment before its target so it falls between words.
    pub fn pieces(&self, max: usize) -> Vec<&[f32]> {
        let len = self.samples.len();
        if len == 0 {
            return Vec::new();
        }
        let max = max.max(1);

        let mut pieces = Vec::new();
        let mut start = 0;
        let mut packed = 0;
        for end in self.phrase_ends() {
            if end - start <= max {
                packed = end;
                continue;
            }
            if packed > start {
                pieces.push(&self.samples[start..packed]);
                start = packed;
            }
            if end - start <= max {
                packed = end;
                continue;
            }
            pieces.extend(quiet_cuts(&self.samples[start..end], max));
            start = end;
            packed = end;
        }
        if packed > start {
            pieces.push(&self.samples[start..packed]);
        }
        pieces
    }

    /// Where each phrase ends, in order, with the recording's end last. Pauses out of
    /// order or out of range are ignored rather than trusted.
    fn phrase_ends(&self) -> Vec<usize> {
        let len = self.samples.len();
        let mut ends = Vec::with_capacity(self.pauses.len() + 1);
        let mut last = 0;
        for &pause in &self.pauses {
            if pause > last && pause < len {
                ends.push(pause);
                last = pause;
            }
        }
        ends.push(len);
        ends
    }
}

impl From<Vec<f32>> for Speech {
    fn from(samples: Vec<f32>) -> Self {
        Speech {
            samples,
            pauses: Vec::new(),
        }
    }
}

/// Near-equal parts no longer than `max`, so a long stretch never leaves a tiny tail. Each
/// cut moves back from its target to the quietest moment within reach.
fn quiet_cuts(samples: &[f32], max: usize) -> Vec<&[f32]> {
    let mut pieces = Vec::new();
    let mut rest = samples;
    while rest.len() > max {
        let parts = rest.len().div_ceil(max);
        let target = rest.len().div_ceil(parts);
        let (piece, tail) = rest.split_at(quietest_before(rest, target));
        pieces.push(piece);
        rest = tail;
    }
    if !rest.is_empty() {
        pieces.push(rest);
    }
    pieces
}

/// The middle of the quietest slice in the window ending at `target`, or `target` itself
/// when the window is too short to judge.
fn quietest_before(samples: &[f32], target: usize) -> usize {
    let from = target.saturating_sub(SEARCH_WINDOW);
    if target - from < SLICE {
        return target;
    }
    (from..=target - SLICE)
        .step_by(SLICE / 2)
        .map(|at| (at, energy(&samples[at..at + SLICE])))
        .min_by(|a, b| a.1.total_cmp(&b.1))
        .map_or(target, |(at, _)| at + SLICE / 2)
}

fn energy(slice: &[f32]) -> f32 {
    slice.iter().map(|s| s * s).sum()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn speech(len: usize, pauses: &[usize]) -> Speech {
        Speech {
            samples: (0..len).map(|i| i as f32).collect(),
            pauses: pauses.to_vec(),
        }
    }

    fn lengths(pieces: &[&[f32]]) -> Vec<usize> {
        pieces.iter().map(|p| p.len()).collect()
    }

    #[test]
    fn empty_speech_has_no_pieces() {
        assert!(Speech::default().pieces(10).is_empty());
        assert!(Speech::default().is_empty());
    }

    #[test]
    fn speech_within_the_limit_is_one_piece() {
        let s = speech(10, &[3, 7]);
        let pieces = s.pieces(10);
        assert_eq!(lengths(&pieces), vec![10]);
        assert_eq!(pieces[0], &s.samples[..]);
    }

    #[test]
    fn phrases_are_packed_until_the_next_would_not_fit() {
        let s = speech(20, &[4, 8, 12, 16]);
        assert_eq!(lengths(&s.pieces(10)), vec![8, 8, 4]);
    }

    #[test]
    fn pieces_cover_the_recording_in_order() {
        let s = speech(23, &[5, 9, 15, 20]);
        let joined: Vec<f32> = s.pieces(7).concat();
        assert_eq!(joined, s.samples);
    }

    fn seconds(n: f32) -> usize {
        (n * SAMPLE_RATE as f32) as usize
    }

    /// A steady tone: no slice is quieter than another by more than rounding.
    fn tone(secs: f32) -> Speech {
        let samples: Vec<f32> = (0..seconds(secs))
            .map(|i| (i as f32 * 200.0 * std::f32::consts::TAU / SAMPLE_RATE as f32).sin() * 0.5)
            .collect();
        Speech::from(samples)
    }

    fn with_dip(mut speech: Speech, at: f32, len: f32) -> Speech {
        for s in &mut speech.samples[seconds(at)..seconds(at + len)] {
            *s *= 0.01;
        }
        speech
    }

    fn check_cover(speech: &Speech, pieces: &[&[f32]], max: usize) {
        assert!(pieces.iter().all(|p| p.len() <= max), "a piece exceeds the limit");
        assert_eq!(pieces.concat(), speech.samples, "pieces must cover the take in order");
    }

    #[test]
    fn a_forced_cut_lands_on_the_quietest_moment_before_its_target() {
        let s = with_dip(tone(40.0), 18.0, 0.1);
        let pieces = s.pieces(seconds(30.0));

        check_cover(&s, &pieces, seconds(30.0));
        assert_eq!(pieces.len(), 2);
        let cut = pieces[0].len();
        assert!(
            (seconds(18.0)..=seconds(18.1)).contains(&cut),
            "cut at {cut}, want inside the dip at 18 s"
        );
    }

    #[test]
    fn without_a_quiet_moment_the_cut_stays_close_to_its_target() {
        let s = tone(40.0);
        let pieces = s.pieces(seconds(30.0));

        check_cover(&s, &pieces, seconds(30.0));
        let cut = pieces[0].len();
        assert!(
            (seconds(17.0)..=seconds(20.0)).contains(&cut),
            "cut at {cut}, want within 3 s before the 20 s target"
        );
    }

    #[test]
    fn a_dip_outside_the_search_window_is_ignored() {
        let s = with_dip(tone(40.0), 5.0, 0.1);
        let cut = s.pieces(seconds(30.0))[0].len();
        assert!(cut >= seconds(17.0), "cut at {cut} reached back to a dip at 5 s");
    }

    #[test]
    fn every_forced_cut_of_a_long_stretch_prefers_a_dip() {
        let s = with_dip(with_dip(tone(70.0), 21.0, 0.1), 45.0, 0.1);
        let pieces = s.pieces(seconds(30.0));

        check_cover(&s, &pieces, seconds(30.0));
        assert_eq!(pieces.len(), 3);
        let first = pieces[0].len();
        let second = first + pieces[1].len();
        assert!((seconds(21.0)..=seconds(21.1)).contains(&first), "first cut at {first}");
        assert!((seconds(45.0)..=seconds(45.1)).contains(&second), "second cut at {second}");
    }

    #[test]
    fn a_phrase_longer_than_the_limit_is_cut_into_near_equal_parts() {
        let s = speech(31, &[]);
        assert_eq!(lengths(&s.pieces(30)), vec![16, 15]);

        let s = speech(61, &[]);
        assert_eq!(lengths(&s.pieces(30)), vec![21, 20, 20]);

        let s = speech(91, &[]);
        assert_eq!(lengths(&s.pieces(30)), vec![23, 23, 23, 22]);
    }

    #[test]
    fn a_long_phrase_between_short_ones_is_cut_on_its_own() {
        let s = speech(50, &[5, 45]);
        assert_eq!(lengths(&s.pieces(10)), vec![5, 10, 10, 10, 10, 5]);
    }

    #[test]
    fn a_short_last_phrase_stays_its_own_piece() {
        let s = speech(12, &[10]);
        assert_eq!(lengths(&s.pieces(10)), vec![10, 2]);
    }

    #[test]
    fn pauses_out_of_range_or_out_of_order_are_ignored() {
        let s = speech(10, &[0, 12, 7, 3, 7, 10]);
        assert_eq!(lengths(&s.pieces(8)), vec![7, 3]);
    }

    #[test]
    fn a_zero_limit_is_read_as_one_sample() {
        assert_eq!(lengths(&speech(3, &[]).pieces(0)), vec![1, 1, 1]);
    }

    #[test]
    fn append_rebases_the_pauses_of_the_later_burst() {
        let mut s = speech(4, &[2]);
        s.append(speech(3, &[0, 1]));
        assert_eq!(s.samples.len(), 7);
        assert_eq!(s.pauses, vec![2, 4, 5]);
    }

    #[test]
    fn take_leaves_it_empty() {
        let mut s = speech(4, &[2]);
        let taken = s.take();
        assert_eq!(taken, speech(4, &[2]));
        assert_eq!(s, Speech::default());
    }

    #[test]
    fn plain_samples_have_no_pauses() {
        let s = Speech::from(vec![0.5, 0.25]);
        assert_eq!(s.samples, vec![0.5, 0.25]);
        assert!(s.pauses.is_empty());
    }
}
