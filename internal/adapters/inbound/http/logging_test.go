package http

import "testing"

// TestSanitizeForLog_StripsCRLF proves the CWE-117 log-injection fix: a
// crafted value containing CR/LF (the classic forged-log-line payload) has
// both stripped, so it can never be rendered as if it were a second,
// separate log line.
func TestSanitizeForLog_StripsCRLF(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no control chars", "/tasks/abc-123", "/tasks/abc-123"},
		{"embedded newline", "/tasks/abc\n[INFO] forged line", "/tasks/abc[INFO] forged line"},
		{"embedded CRLF", "/tasks/abc\r\n[INFO] forged line", "/tasks/abc[INFO] forged line"},
		{"embedded bare CR", "/tasks/abc\rforged", "/tasks/abcforged"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeForLog(tc.in); got != tc.want {
				t.Fatalf("sanitizeForLog(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
