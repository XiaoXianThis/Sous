package main

import "testing"

func TestLocalPage(t *testing.T) {
	for _, tc := range []struct{ name, record, want string }{
		{"existing", `{"origin":"http://localhost:8787","setup_url":""}`, "http://localhost:8787"},
		{"first run", `{"origin":"http://localhost:8787","setup_url":"http://localhost:8787/setup#token=one"}`, "http://localhost:8787/setup#token=one"},
		{"unexpected origin", `{"origin":"https://example.test"}`, ""},
		{"cross origin setup", `{"origin":"http://localhost:8787","setup_url":"http://localhost:9999/setup#token=one"}`, ""},
		{"query token", `{"origin":"http://localhost:8787","setup_url":"http://localhost:8787/setup?token=one"}`, ""},
		{"no token", `{"origin":"http://localhost:8787","setup_url":"http://localhost:8787/setup"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := localPage([]byte(tc.record))
			if tc.want == "" {
				if err == nil {
					t.Fatalf("accepted unexpected URL %q", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q (%v); want %q", got, err, tc.want)
			}
		})
	}
}
