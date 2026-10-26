package pcm

import "testing"

func TestJoinSpacesOnlyWhereTheScriptDoes(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{[]string{"hello there", " and more "}, "hello there and more"},
		{[]string{"今天天气", "很好。"}, "今天天气很好。"},
		{[]string{"終わりました。", "次に"}, "終わりました。次に"},
		{[]string{"สวัสดี", "ครับ"}, "สวัสดีครับ"},
		{[]string{"안녕하세요", "반갑습니다"}, "안녕하세요 반갑습니다"},
		{[]string{"Go", "语言"}, "Go语言"},
		{[]string{"", " ", "only"}, "only"},
	}
	for _, c := range cases {
		if got := Join(c.parts); got != c.want {
			t.Errorf("Join(%q) = %q, want %q", c.parts, got, c.want)
		}
	}
}
