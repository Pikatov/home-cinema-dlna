package main

import "testing"

func TestEscapeXMLText(t *testing.T) {
	in := `Movie & "Quotes" <Tag> 'One'`
	want := "Movie &amp; &quot;Quotes&quot; &lt;Tag&gt; &apos;One&apos;"
	if got := escapeXMLText(in); got != want {
		t.Fatalf("escapeXMLText() = %q, want %q", got, want)
	}
}
