package main

import (
	"strings"
	"testing"
)

// mapGetenv returns a getenv-style function backed by a map.
func mapGetenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestParseEncodeParams_Defaults: unset vars → hardcoded defaults, no warnings.
func TestParseEncodeParams_Defaults(t *testing.T) {
	ep, warns := parseEncodeParams(mapGetenv(nil))
	if len(warns) != 0 {
		t.Fatalf("expected no warnings, got %v", warns)
	}
	def := defaultEncodeParams()
	if ep != def {
		t.Fatalf("expected defaults %+v, got %+v", def, ep)
	}
	if ep.VideoBitrate != "300k" || ep.GOP != "60" || ep.AudioBitrate != "48k" || ep.Preset != "ultrafast" {
		t.Fatalf("default values wrong: %+v", ep)
	}
}

// TestParseEncodeParams_ValidInjection (§단언 J): valid values are injected verbatim.
func TestParseEncodeParams_ValidInjection(t *testing.T) {
	ep, warns := parseEncodeParams(mapGetenv(map[string]string{
		"ENCODE_VIDEO_BITRATE": "500k",
		"ENCODE_GOP":           "30",
		"ENCODE_AUDIO_BITRATE": "64k",
		"ENCODE_PRESET":        "veryfast",
	}))
	if len(warns) != 0 {
		t.Fatalf("expected no warnings for valid values, got %v", warns)
	}
	want := encodeParams{VideoBitrate: "500k", GOP: "30", AudioBitrate: "64k", Preset: "veryfast"}
	if ep != want {
		t.Fatalf("want %+v, got %+v", want, ep)
	}
}

// TestParseEncodeParams_FallbackTable (§단언 J-2): each invalid var falls back to
// its own default with a warning, independently of the others.
func TestParseEncodeParams_FallbackTable(t *testing.T) {
	cases := []struct {
		name       string
		env        map[string]string
		want       encodeParams
		wantWarnOn string // substring expected in a warning; "" = no warning
	}{
		{
			name: "invalid GOP non-numeric",
			env:  map[string]string{"ENCODE_GOP": "xyz"},
			want: encodeParams{"300k", "60", "48k", "ultrafast"},
			wantWarnOn: "ENCODE_GOP",
		},
		{
			name: "GOP zero rejected",
			env:  map[string]string{"ENCODE_GOP": "0"},
			want: encodeParams{"300k", "60", "48k", "ultrafast"},
			wantWarnOn: "ENCODE_GOP",
		},
		{
			name: "GOP negative rejected",
			env:  map[string]string{"ENCODE_GOP": "-5"},
			want: encodeParams{"300k", "60", "48k", "ultrafast"},
			wantWarnOn: "ENCODE_GOP",
		},
		{
			name: "unknown preset rejected",
			env:  map[string]string{"ENCODE_PRESET": "warpspeed"},
			want: encodeParams{"300k", "60", "48k", "ultrafast"},
			wantWarnOn: "ENCODE_PRESET",
		},
		{
			name: "zero video bitrate rejected",
			env:  map[string]string{"ENCODE_VIDEO_BITRATE": "0k"},
			want: encodeParams{"300k", "60", "48k", "ultrafast"},
			wantWarnOn: "ENCODE_VIDEO_BITRATE",
		},
		{
			name: "garbage audio bitrate rejected",
			env:  map[string]string{"ENCODE_AUDIO_BITRATE": "loud"},
			want: encodeParams{"300k", "60", "48k", "ultrafast"},
			wantWarnOn: "ENCODE_AUDIO_BITRATE",
		},
		{
			name: "plain integer bitrate accepted",
			env:  map[string]string{"ENCODE_VIDEO_BITRATE": "800"},
			want: encodeParams{"800", "60", "48k", "ultrafast"},
			wantWarnOn: "",
		},
		{
			name: "independent fallback: invalid GOP, valid preset",
			env:  map[string]string{"ENCODE_GOP": "abc", "ENCODE_PRESET": "slow"},
			want: encodeParams{"300k", "60", "48k", "slow"},
			wantWarnOn: "ENCODE_GOP",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ep, warns := parseEncodeParams(mapGetenv(tc.env))
			if ep != tc.want {
				t.Fatalf("params: want %+v, got %+v", tc.want, ep)
			}
			if tc.wantWarnOn == "" {
				if len(warns) != 0 {
					t.Fatalf("expected no warnings, got %v", warns)
				}
				return
			}
			found := false
			for _, w := range warns {
				if strings.Contains(w, tc.wantWarnOn) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected a warning mentioning %q, got %v", tc.wantWarnOn, warns)
			}
		})
	}
}

// TestBuildFFmpegArgs_InjectsEncodeValues (§단언 J): the resulting FFmpeg argument
// list reflects the injected encode params verbatim, with fixed H.264/AAC codecs.
func TestBuildFFmpegArgs_InjectsEncodeValues(t *testing.T) {
	ep := encodeParams{VideoBitrate: "500k", GOP: "30", AudioBitrate: "64k", Preset: "veryfast"}
	args := buildFFmpegArgs("in.mp4", "rtmp://x/live/k", true, ep)

	assertArgPair(t, args, "-b:v", "500k")
	assertArgPair(t, args, "-g", "30")
	assertArgPair(t, args, "-b:a", "64k")
	assertArgPair(t, args, "-preset", "veryfast")
	// Codec normalization is fixed regardless of encode params (§단언 E).
	assertArgPair(t, args, "-c:v", "libx264")
	assertArgPair(t, args, "-c:a", "aac")
	// Local file → infinite loop.
	if !containsArg(args, "-stream_loop") {
		t.Fatalf("expected -stream_loop for local file, args=%v", args)
	}
}

// TestBuildFFmpegArgs_DefaultsWhenUnset (§단언 J): default encode params produce
// the original hardcoded argument values (behaviour unchanged when unset).
func TestBuildFFmpegArgs_DefaultsWhenUnset(t *testing.T) {
	ep, _ := parseEncodeParams(mapGetenv(nil))
	args := buildFFmpegArgs("in.mp4", "rtmp://x/live/k", false, ep)
	assertArgPair(t, args, "-b:v", "300k")
	assertArgPair(t, args, "-g", "60")
	assertArgPair(t, args, "-b:a", "48k")
	assertArgPair(t, args, "-preset", "ultrafast")
	if containsArg(args, "-stream_loop") {
		t.Fatalf("did not expect -stream_loop for non-local source, args=%v", args)
	}
}

func containsArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// assertArgPair asserts that flag is immediately followed by value in args.
func assertArgPair(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			if args[i+1] != value {
				t.Fatalf("arg %s: want %q, got %q (args=%v)", flag, value, args[i+1], args)
			}
			return
		}
	}
	t.Fatalf("flag %s not found in args %v", flag, args)
}
