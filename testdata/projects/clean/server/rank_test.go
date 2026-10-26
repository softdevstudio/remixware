package server

import "testing"

func TestStatusText(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{200, "ok"},
		{301, "redirect"},
		{302, "redirect"},
		{401, "refused"},
		{403, "refused"},
		{404, "missing"},
		{500, "server error"},
		{503, "server error"},
		{418, "unexpected"},
	}
	for _, tc := range tests {
		if got := statusText(tc.code); got != tc.want {
			t.Errorf("statusText(%d) = %q, want %q", tc.code, got, tc.want)
		}
	}
}
