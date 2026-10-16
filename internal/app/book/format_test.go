package bookapp

import "testing"

func TestFormatBookNameStripsDecorations(t *testing.T) {
	tests := map[string]string{
		"斗破苍穹":         "斗破苍穹",
		"斗破苍穹 作者:天蚕土豆": "斗破苍穹",
		"斗破苍穹 作者：天蚕土豆": "斗破苍穹",
		"斗破苍穹 天蚕土豆 著":  "斗破苍穹",
		"  斗破苍穹  ":     "斗破苍穹",
	}
	for input, want := range tests {
		if got := FormatBookName(input); got != want {
			t.Fatalf("FormatBookName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFormatBookAuthorStripsPrefixAndSuffix(t *testing.T) {
	tests := map[string]string{
		"天蚕土豆":       "天蚕土豆",
		"作者:天蚕土豆":    "天蚕土豆",
		"作者：天蚕土豆":    "天蚕土豆",
		" 作者 : 天蚕土豆": "天蚕土豆",
		"天蚕土豆 著":     "天蚕土豆",
	}
	for input, want := range tests {
		if got := FormatBookAuthor(input); got != want {
			t.Fatalf("FormatBookAuthor(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFormatWordCountMatchesLegadoSemantics(t *testing.T) {
	tests := map[string]string{
		"":      "",
		"0":     "",
		"-5":    "",
		"12345": "1.2万字",
		"15000": "1.5万字",
		"20000": "2万字",
		"10001": "1万字",
		"10000": "10000字",
		"9999":  "9999字",
		"1.2万":  "1.2万",
		"约十万":   "约十万",
	}
	for input, want := range tests {
		if got := FormatWordCount(input); got != want {
			t.Fatalf("FormatWordCount(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFormatIntroStripsHTML(t *testing.T) {
	tests := map[string]string{
		"":                          "",
		"少年故事":                      "少年故事",
		"<p>少年&nbsp;故事</p>":         "\u3000\u3000\u3000\u3000少年 故事\n\u3000\u3000",
		"<div>第一段<br/>第二段</div>":    "\u3000\u3000\u3000\u3000第一段\n\u3000\u3000第二段\n\u3000\u3000",
		"<span>纯文本</span><!--注释-->": "纯文本",
	}
	for input, want := range tests {
		if got := FormatIntro(input); got != want {
			t.Fatalf("FormatIntro(%q) = %q, want %q", input, got, want)
		}
	}
}
