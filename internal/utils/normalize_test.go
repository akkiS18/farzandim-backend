package utils

import (
	"testing"
)

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"+998 90 123 45 67", "998901234567"},
		{"90-123-45-67", "998901234567"},
		{"(90) 123 45 67", "998901234567"},
		{"998901234567", "998901234567"},
		{"", ""},
	}

	for _, tc := range tests {
		actual := NormalizePhone(tc.input)
		if actual != tc.expected {
			t.Errorf("NormalizePhone(%q) = %q; want %q", tc.input, actual, tc.expected)
		}
	}
}

func TestNormalizeDocumentNo(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"AB 1234567", "AB1234567"},
		{"ab1234567", "AB1234567"},
		{"I-NA 1234567", "I-NA 1234567"},
		{"1-NA 1234567", "I-NA 1234567"},
		{"l-na.1234567", "I-NA 1234567"},
		{"ina 1234567", "I-NA 1234567"},
		{"yo'q", "yo'q"},
		{"-", "-"},
		{"", ""},
	}

	for _, tc := range tests {
		actual := NormalizeDocumentNo(tc.input)
		if actual != tc.expected {
			t.Errorf("NormalizeDocumentNo(%q) = %q; want %q", tc.input, actual, tc.expected)
		}
	}
}

func TestSplitFIO(t *testing.T) {
	last, first, middle := SplitFIO("Aliyev Vali G'ani o'g'li")
	if last != "Aliyev" || first != "Vali" || middle != "G'ani o'g'li" {
		t.Errorf("SplitFIO failed: got %q, %q, %q", last, first, middle)
	}

	last2, first2, middle2 := SplitFIO("Karimov")
	if last2 != "Karimov" || first2 != "" || middle2 != "" {
		t.Errorf("SplitFIO failed for single name: got %q, %q, %q", last2, first2, middle2)
	}
}
