#!/usr/bin/env python3
"""Generate Encre's icon set: an ink drop, artwork only, no tile behind it."""

import io
import os
import struct

from PIL import Image, ImageDraw

SUPERSAMPLE = 8
VIEW = 100  # artwork viewport, scaled to the render size

BODY = (59, 130, 246)
HIGHLIGHT = (147, 197, 253)

# Clear of the canvas edge so the drop is not clipped by a rounded frame.
MARGIN = 0.04

# Four files are consumed by anything: icon.png (Fyne metadata, the Linux
# packages, the window icon), icon_1024.png (the macOS bundle, which wants a
# retina source), icon.ico (Windows) and tray.png (the Linux tray). The
# per-size renders behind the .ico are built in memory and never written.
ICO_SIZES = [16, 20, 24, 32, 40, 48, 64, 128, 256]

# Linux panels ask for 16 to 24 px. The systray library corrupts the colour of
# every translucent pixel it sends, so the tray image has hard edges and is
# drawn at twice the common panel size: halving it is what smooths the edges.
TRAY_SIZE = 32

DROP = (
    (50, 5),
    [("C", (72, 34), (88, 52), (88, 66)),
     ("C", (88, 87), (71, 97), (50, 97)),
     ("C", (29, 97), (12, 87), (12, 66)),
     ("C", (12, 52), (28, 34), (50, 5))],
)

# The lit edge running down the left of the drop.
SHEEN = (
    (50, 5),
    [("C", (36, 32), (24, 50), (24, 66)),
     ("C", (24, 80), (33, 90), (46, 94)),
     ("C", (34, 86), (30, 76), (30, 66)),
     ("C", (30, 50), (40, 30), (50, 5))],
)


def cubic(p0, c1, c2, p3, steps=64):
    for i in range(1, steps + 1):
        t = i / steps
        u = 1 - t
        yield (
            u**3 * p0[0] + 3 * u**2 * t * c1[0] + 3 * u * t**2 * c2[0] + t**3 * p3[0],
            u**3 * p0[1] + 3 * u**2 * t * c1[1] + 3 * u * t**2 * c2[1] + t**3 * p3[1],
        )


def flatten(path):
    start, segments = path
    points, current = [start], start
    for kind, *args in segments:
        if kind == "C":
            points.extend(cubic(current, *args))
        else:
            points.append(args[0])
        current = args[-1]
    return points


def outline(path, size):
    k = size / VIEW
    return [(x * k, y * k) for x, y in flatten(path)]


def artwork(size, body, sheen):
    layer = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    draw = ImageDraw.Draw(layer)
    draw.polygon(outline(DROP, size), fill=body + (255,))
    if sheen:
        draw.polygon(outline(SHEEN, size), fill=sheen + (255,))
    return layer


def fitted(layer, size, margin):
    layer = layer.crop(layer.getbbox())

    inner = size * (1 - margin * 2)
    scale = min(inner / layer.width, inner / layer.height)
    layer = layer.resize((max(round(layer.width * scale), 1), max(round(layer.height * scale), 1)),
                         Image.LANCZOS)

    icon = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    icon.alpha_composite(layer, ((size - layer.width) // 2, (size - layer.height) // 2))
    return icon


def app_icon(size):
    work = size * SUPERSAMPLE
    return fitted(artwork(work, BODY, HIGHLIGHT), work, MARGIN).resize((size, size), Image.LANCZOS)


def tray_icon():
    icon = app_icon(TRAY_SIZE)
    icon.putalpha(icon.split()[-1].point(lambda v: 255 if v >= 128 else 0))
    return icon


# Written by hand: Pillow leaves out the AND mask every frame must end with, and Windows
# decoders disagree on what to do with the bytes that follow. Frames below 256 px are
# DIBs, which every Windows path reads; the 256 px frame is PNG, as the shell expects.
def ico(frames):
    entries, payload = [], b""
    offset = 6 + 16 * len(frames)
    for image in frames:
        data = png_frame(image) if image.width == 256 else dib_frame(image)
        entries.append(struct.pack("<BBBBHHII", image.width % 256, image.height % 256, 0, 0, 1, 32,
                                   len(data), offset + len(payload)))
        payload += data
    return struct.pack("<HHH", 0, 1, len(frames)) + b"".join(entries) + payload


def png_frame(image):
    out = io.BytesIO()
    image.save(out, "PNG")
    return out.getvalue()


def dib_frame(image):
    width, height = image.size
    header = struct.pack("<IiiHHIIiiII", 40, width, height * 2, 1, 32, 0, width * height * 4, 0, 0, 0, 0)
    pixels = b"".join(
        bytes((b, g, r, a))
        for y in range(height - 1, -1, -1)
        for (r, g, b, a) in (image.getpixel((x, y)) for x in range(width))
    )
    row_bytes = (width + 31) // 32 * 4
    mask = b"".join(mask_row(image, y, row_bytes) for y in range(height - 1, -1, -1))
    return header + pixels + mask


def mask_row(image, y, row_bytes):
    bits = 0
    for x in range(image.width):
        bits = (bits << 1) | (1 if image.getpixel((x, y))[3] == 0 else 0)
    bits <<= row_bytes * 8 - image.width
    return bits.to_bytes(row_bytes, "big")


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    assets = os.path.join(root, "assets")
    os.makedirs(assets, exist_ok=True)

    icons = {size: app_icon(size) for size in ICO_SIZES}

    icons[256].save(os.path.join(assets, "icon.png"))
    app_icon(1024).save(os.path.join(assets, "icon_1024.png"))
    tray_icon().save(os.path.join(assets, "tray.png"))

    with open(os.path.join(assets, "icon.ico"), "wb") as f:
        f.write(ico([icons[s] for s in ICO_SIZES]))

    print("icon.png       256 px, app and Linux packages")
    print("icon_1024.png  1024 px, macOS bundle")
    print(f"icon.ico       {ICO_SIZES}, BMP frames, Windows")
    print(f"tray.png       {TRAY_SIZE} px, hard edges, Linux tray")


if __name__ == "__main__":
    main()
