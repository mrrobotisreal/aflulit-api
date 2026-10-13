package asr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testModel     = "ivrit-ai/whisper-large-v3-turbo-ct2"
	hebrewResult  = `{"text":" שלום עולם ","language":"he","segments":[{"start":0,"end":0.5,"text":"שלום עולם","words":[{"word":" שלום","start":0.0,"end":0.25},{"word":" עולם","start":0.25,"end":0.5}]}]}`
	englishResult = `{"text":" good morning ","language":"en","segments":[]}`
)

// fixture writes a shell-script fake whisper-ctranslate2 that asserts the
// argument vector and environment the way Ilan's cliFixture does, then runs
// body (which may write "$out/utterance.json").
func fixture(t *testing.T, body string, cfg func(*CLIConfig)) *CLIRunner {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "whisper-ctranslate2")
	script := `#!/bin/sh
set -eu
if [ "${1:-}" = "--help" ]; then
  echo "usage: whisper-ctranslate2 --model --device --compute_type --language --task --output_dir --output_format --verbose --vad_filter --condition_on_previous_text --beam_size --temperature --temperature_increment_on_fallback --word_timestamps --model_directory --model_dir --local_files_only"
  exit 0
fi
test -z "${SYNCD_TOKEN:-}"
test -z "${ASR_TOKEN:-}"
input="$1"
shift
out=''
while [ "$#" -gt 0 ]; do
 case "$1" in
 --model) test "$2" = '` + testModel + `' ;;
 --language) test "$2" = he -o "$2" = en ;;
 --task) test "$2" = transcribe ;;
 --device) test "$2" = cuda ;;
 --compute_type) test "$2" = float16 ;;
 --beam_size) test "$2" = 5 ;;
 --vad_filter) test "$2" = True ;;
 --output_dir) out="$2" ;;
 --initial_prompt|--prefix|--hotwords) exit 91 ;;
 esac
 shift 2
done
test -f "$input"
printf '%s\n' "$input" > "$out/argv-seen"
` + body
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	c := CLIConfig{
		Binary: bin, Model: testModel, Device: "cuda", ComputeType: "float16", BeamSize: 5, VADFilter: true,
		MaxParallel: 1, MaxAudioSeconds: 2, SecretEnvKeys: []string{"SYNCD_TOKEN", "ASR_TOKEN"},
	}
	if cfg != nil {
		cfg(&c)
	}
	return NewCLIRunner(c)
}

func voiceWAV() []byte { return EncodeWAV(pcmSeconds(0.5, 800)) }

func writeResult(json string) string {
	return `printf '%s' '` + json + `' > "$out/utterance.json"
`
}

func leakedWorkspaces(t *testing.T, root string) int {
	t.Helper()
	entries, _ := os.ReadDir(root)
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "aflulit-asr-") {
			n++
		}
	}
	return n
}

func TestTranscribeArgvEnvResultAndCleanup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("SYNCD_TOKEN", "must-not-reach-child-0123456789")
	t.Setenv("ASR_TOKEN", "also-private-0123456789abcdef")
	r := fixture(t, writeResult(hebrewResult), func(c *CLIConfig) { c.WordTimestamps = true })

	res, err := r.Transcribe(context.Background(), voiceWAV(), "he")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "שלום עולם" || res.Model != testModel || res.DurationMs != 500 || res.NoSpeech {
		t.Fatalf("bad result: %+v", res)
	}
	if len(res.Words) != 2 || res.Words[0].Word != "שלום" || res.Words[1].StartMs != 250 || res.Words[1].EndMs != 500 {
		t.Fatalf("words not mapped: %+v", res.Words)
	}
	if leakedWorkspaces(t, root) != 0 {
		t.Fatal("workspace leaked after success")
	}
}

func TestArgvExact(t *testing.T) {
	r := fixture(t, "", nil)
	got := r.args("in.wav", "out", "en")
	want := []string{"in.wav", "--model", testModel, "--device", "cuda", "--compute_type", "float16",
		"--language", "en", "--task", "transcribe", "--output_dir", "out", "--output_format", "json",
		"--verbose", "False", "--vad_filter", "True", "--condition_on_previous_text", "False",
		"--beam_size", "5", "--temperature", "0", "--temperature_increment_on_fallback", "None"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("argv:\n got %q\nwant %q", got, want)
	}
	for _, forbidden := range []string{"--initial_prompt", "--prefix", "--hotwords"} {
		for _, a := range got {
			if a == forbidden {
				t.Fatalf("%s must never be passed", forbidden)
			}
		}
	}
	full := fixture(t, "", func(c *CLIConfig) {
		c.WordTimestamps = true
		c.ModelDirectory = "/models/x"
		c.ModelCache = "/cache"
		c.LocalFilesOnly = true
		c.VADFilter = false
	}).args("in.wav", "out", "he")
	s := strings.Join(full, " ")
	for _, want := range []string{"--word_timestamps True", "--model_directory /models/x", "--model_dir /cache", "--local_files_only True", "--vad_filter False"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %q", want, s)
		}
	}
}

func TestLanguageConstraint(t *testing.T) {
	en := fixture(t, writeResult(englishResult), nil)
	res, err := en.Transcribe(context.Background(), voiceWAV(), "en")
	if err != nil || res.Text != "good morning" {
		t.Fatalf("English decode failed: %v %+v", err, res)
	}
	// The CLI reported en for a he request → rejected (wrong-language JSON).
	if _, err := en.Transcribe(context.Background(), voiceWAV(), "he"); err == nil {
		t.Fatal("CLI reporting en accepted for a he request")
	}
	he := fixture(t, writeResult(hebrewResult), nil)
	if _, err := he.Transcribe(context.Background(), voiceWAV(), "en"); err == nil {
		t.Fatal("CLI reporting he accepted for an en request")
	}
	if _, err := he.Transcribe(context.Background(), voiceWAV(), "fr"); !errors.Is(err, ErrUnsupportedLanguage) {
		t.Fatalf("fr must be refused before the CLI runs: %v", err)
	}
	if _, err := he.Transcribe(context.Background(), voiceWAV(), ""); !errors.Is(err, ErrUnsupportedLanguage) {
		t.Fatal("empty language is the handler's job to default; the runner refuses it")
	}
}

func TestParseResultRejectsMalformed(t *testing.T) {
	base := Result{DurationMs: 1000}
	for _, bad := range []string{`{}`, `not json`, `{"text":"a","language":"he","segments":[{"start":2,"end":1}]}`,
		`{"text":"a","language":"he","segments":[{"start":0,"end":9}]}`, `{"language":"he"}`} {
		if _, err := parseResult([]byte(bad), "he", base); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// No words anywhere is fine: Words stays nil and is omitted from JSON.
	res, err := parseResult([]byte(`{"text":" כן ","language":"he","segments":[{"start":0,"end":0.4,"text":"כן"}]}`), "he", base)
	if err != nil || res.Text != "כן" || res.Words != nil {
		t.Fatalf("plain segments: %v %+v", err, res)
	}
}

func TestSilenceAndLimitsNeverRunCLI(t *testing.T) {
	r := fixture(t, "exit 70\n", nil)
	res, err := r.Transcribe(context.Background(), EncodeWAV(pcmSeconds(0.5, 0)), "he")
	if err != nil || !res.NoSpeech || res.Text != "" || res.DurationMs != 500 {
		t.Fatalf("silence: %v %+v", err, res)
	}
	res, err = r.Transcribe(context.Background(), EncodeWAV(pcmSeconds(0.05, 800)), "he")
	if err != nil || !res.NoSpeech {
		t.Fatalf("sub-100ms clip must short-circuit: %v %+v", err, res)
	}
	if _, err := r.Transcribe(context.Background(), EncodeWAV(pcmSeconds(2.5, 800)), "he"); !errors.Is(err, ErrTooLong) {
		t.Fatalf("over MaxAudioSeconds: %v", err)
	}
	if _, err := r.Transcribe(context.Background(), []byte("OggS....."), "he"); !errors.Is(err, ErrUnsupportedAudio) {
		t.Fatalf("non-WAV: %v", err)
	}
}

func TestFailureAndMissingResultCleanUp(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	failing := fixture(t, "echo 'RuntimeError: CUDA driver version is insufficient' >&2\nexit 70\n", nil)
	if _, err := failing.Transcribe(context.Background(), voiceWAV(), "he"); err == nil {
		t.Fatal("CLI failure accepted")
	} else if !strings.Contains(err.Error(), "exit status 70") || !strings.Contains(err.Error(), "CUDA driver version is insufficient") {
		t.Fatalf("failure must carry the exit code and the stderr tail: %v", err)
	}
	missing := fixture(t, "exit 0\n", nil)
	if _, err := missing.Transcribe(context.Background(), voiceWAV(), "he"); err == nil {
		t.Fatal("exit 0 without JSON accepted")
	}
	if leakedWorkspaces(t, root) != 0 {
		t.Fatal("workspace leaked after failure")
	}
}

func TestCancellationKillsProcessGroupAndReleasesSlot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	// A grandchild (sleep) in the group: a plain kill of the shell would
	// leave it running and WaitDelay would expire; the group kill ends it.
	r := fixture(t, "sleep 30 &\nwait\n"+writeResult(hebrewResult), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := r.Transcribe(ctx, voiceWAV(), "he")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("cancelled process did not end promptly (group kill failed?)")
	}
	if len(r.gate) != 0 {
		t.Fatal("GPU slot not released after cancel")
	}
	if leakedWorkspaces(t, root) != 0 {
		t.Fatal("workspace leaked after cancel")
	}
}

func TestBusyIsImmediate(t *testing.T) {
	r := fixture(t, writeResult(hebrewResult), nil)
	r.gate <- struct{}{}
	defer func() { <-r.gate }()
	start := time.Now()
	if _, err := r.Transcribe(context.Background(), voiceWAV(), "he"); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("busy must not queue")
	}
}

func TestCheckNamesMissingFlag(t *testing.T) {
	ok := fixture(t, "", func(c *CLIConfig) { c.WordTimestamps = true; c.LocalFilesOnly = true })
	if err := ok.Check(context.Background()); err != nil {
		t.Fatalf("full help must pass: %v", err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "whisper-ctranslate2")
	_ = os.WriteFile(bin, []byte("#!/bin/sh\necho 'usage: --model --device --language'\n"), 0o700)
	old := NewCLIRunner(CLIConfig{Binary: bin, Model: "large-v3"})
	err := old.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--compute_type") {
		t.Fatalf("missing flag must be named: %v", err)
	}
	gone := NewCLIRunner(CLIConfig{Binary: filepath.Join(dir, "nope"), Model: "large-v3"})
	if err := gone.Ready(context.Background()); err == nil {
		t.Fatal("missing binary must fail Ready")
	}
}

// Golden: the JSON shape of Result is the response contract T14 consumes
// (ADR-0008 §2): text, optional words[{word,startMs,endMs}], model, durationMs.
func TestResultJSONGolden(t *testing.T) {
	withWords := Result{Text: "שלום עולם", Model: testModel, DurationMs: 500,
		Words: []Word{{Word: "שלום", StartMs: 0, EndMs: 250}, {Word: "עולם", StartMs: 250, EndMs: 500}}, NoSpeech: false}
	got, _ := json.Marshal(withWords)
	want := `{"text":"שלום עולם","words":[{"word":"שלום","startMs":0,"endMs":250},{"word":"עולם","startMs":250,"endMs":500}],"model":"ivrit-ai/whisper-large-v3-turbo-ct2","durationMs":500}`
	if string(got) != want {
		t.Fatalf("golden mismatch:\n got %s\nwant %s", got, want)
	}
	silent, _ := json.Marshal(Result{Model: "large-v3", DurationMs: 500, NoSpeech: true})
	if string(silent) != `{"text":"","model":"large-v3","durationMs":500}` {
		t.Fatalf("silent golden: %s", silent)
	}
}
