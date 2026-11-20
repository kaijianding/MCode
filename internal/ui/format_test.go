package mcodeui

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{-5 * time.Second, "0s"},
		{500 * time.Millisecond, "0s"},
		{5 * time.Second, "5s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m 00s"},
		{65 * time.Second, "1m 05s"},
		{90 * time.Second, "1m 30s"},
		{3599 * time.Second, "59m 59s"},
		{3600 * time.Second, "1h 00m"},
		{3661 * time.Second, "1h 01m"},
	}
	for _, tc := range cases {
		if got := formatDuration(tc.d); got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestFormatTokenCountUnified(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{950, "950"},
		{1000, "1k"},
		{1500, "1.5k"},
		{1000000, "1M"},
		{1500000, "1.5M"},
		{1000000000, "1B"},
		{1500000000, "1.5B"},
		{-950, "-950"},
		{-1500, "-1.5k"},
		{-1500000, "-1.5M"},
	}
	for _, tc := range cases {
		if got := formatTokenCount(tc.n); got != tc.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestFormatFileSizeUnified(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1 KB"},
		{1536, "1.5 KB"},
		{2048, "2 KB"},
		{100 * 1024, "100 KB"},
		{1024 * 1024, "1 MB"},
		{1024 * 1024 * 1024, "1 GB"},
		{1024 * 1024 * 1024 * 1024, "1 TB"},
	}
	for _, tc := range cases {
		if got := formatFileSize(tc.bytes); got != tc.want {
			t.Errorf("formatFileSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}
