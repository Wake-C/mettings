package meeting

import (
	"strings"
	"testing"
)

func TestCorrectionsRespectASCIIAndAliasesOnlyTouchSpeaker(t *testing.T) {
	got := Correct("177 and 77 麦狗", "77=宁\n麦狗=Mygo")
	if got != "177 and 宁 Mygo" {
		t.Fatal(got)
	}
	got = MapSpeakers("**[00:00:03] 1**：1 个方案\n", map[string]string{"1": "许宁"})
	if !strings.Contains(got, "许宁**：1 个方案") {
		t.Fatal(got)
	}
}
func TestCosineRejectsDifferentVoiceSpaces(t *testing.T) {
	if Similarity([]float64{1, 0}, []float64{1}) != 0 {
		t.Fatal("different vector dimensions matched")
	}
	if Similarity([]float64{1, 0}, []float64{1, 0}) < .99 {
		t.Fatal("same voice did not match")
	}
}
