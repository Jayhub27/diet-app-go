package templates

import "testing"

func TestFormatMessage(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "   \n ", ""},
		{"plain", "hello", "<p>hello</p>"},
		{"bold", "**Chickpea bowl**", "<p><strong>Chickpea bowl</strong></p>"},
		{"italic", "a *little* heat", "<p>a <em>little</em> heat</p>"},
		{"code", "set `model_id` first", "<p>set <code>model_id</code> first</p>"},
		{
			"bullets",
			"- 1 cup chickpeas\n- 2 cups spinach",
			"<ul><li>1 cup chickpeas</li><li>2 cups spinach</li></ul>",
		},
		{
			"numbered",
			"1. chop\n2. cook",
			"<ol><li>chop</li><li>cook</li></ol>",
		},
		{
			"heading and paragraph",
			"## Dinner\nTry this.",
			"<h2>Dinner</h2><p>Try this.</p>",
		},
		{
			"paragraph breaks",
			"first\nstill first\n\nsecond",
			"<p>first<br>still first</p><p>second</p>",
		},
		{
			"link",
			"see [docs](https://example.com/x)",
			`<p>see <a href="https://example.com/x" target="_blank" rel="noopener noreferrer">docs</a></p>`,
		},
		{
			"escapes html",
			`<script>alert("x")</script>`,
			"<p>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;</p>",
		},
		{
			"code escapes html",
			"`<b>`",
			"<p><code>&lt;b&gt;</code></p>",
		},
		{
			"javaScript link is not a link",
			"[x](javascript:alert(1))",
			"<p>[x](javascript:alert(1))</p>",
		},
		{
			"multiplication is not italic",
			"2 * 3 * 4",
			"<p>2 * 3 * 4</p>",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(FormatMessage(tc.in)); got != tc.want {
				t.Fatalf("FormatMessage(%q)\n got: %s\nwant: %s", tc.in, got, tc.want)
			}
		})
	}
}
