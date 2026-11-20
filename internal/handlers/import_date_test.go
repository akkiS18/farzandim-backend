package handlers

import "testing"

func TestParseImportDate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // empty string expects nil
	}{
		{name: "excel_serial_43471", input: "43471", want: "2019-01-06"},
		{name: "excel_serial_43554", input: "43554", want: "2019-03-30"},
		{name: "excel_serial_43816", input: "43816", want: "2019-12-17"},
		{name: "excel_serial_float_zero", input: "43471.0", want: "2019-01-06"},
		{name: "excel_serial_float_half", input: "43471.5", want: "2019-01-06"},
		{name: "iso_date", input: "2019-01-06", want: "2019-01-06"},
		{name: "dmy_dot", input: "06.01.2019", want: "2019-01-06"},
		{name: "dmy_short_dot", input: "6.1.2019", want: "2019-01-06"},
		{name: "dmy_slash", input: "06/01/2019", want: "2019-01-06"},
		{name: "ymd_slash", input: "2019/01/06", want: "2019-01-06"},
		{name: "dmy_comma", input: "06,01,2019", want: "2019-01-06"},
		{name: "dmy_dash", input: "06-01-2019", want: "2019-01-06"},
		{name: "empty", input: "", want: ""},
		{name: "dash", input: "-", want: ""},
		{name: "year_only_4_digits", input: "2019", want: ""},
		{name: "phone_number", input: "998901112233", want: ""},
		{name: "invalid_string", input: "not-a-date", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseImportDate(tc.input)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("parseImportDate(%q) = %s, want nil", tc.input, got.Format("2006-01-02"))
				}
				return
			}
			if got == nil {
				t.Fatalf("parseImportDate(%q) = nil, want %s", tc.input, tc.want)
			}
			if gotStr := got.Format("2006-01-02"); gotStr != tc.want {
				t.Fatalf("parseImportDate(%q) = %s, want %s", tc.input, gotStr, tc.want)
			}
		})
	}
}
