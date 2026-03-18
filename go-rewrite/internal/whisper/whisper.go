// Package whisper provides audio transcription via local whisper.cpp binary or
// a remote STT API endpoint.
package whisper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/claudeclaw/claudeclaw/internal/config"
)

// ---------------------------------------------------------------------------
// Constants & types
// ---------------------------------------------------------------------------

const whisperModel = "base.en"

const modelURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin"

const defaultSTTModel = "Systran/faster-whisper-large-v3"

const whisperTimeout = 30 * time.Second

// BinarySource describes where to download the whisper-cli binary for a
// given platform.
type BinarySource struct {
	URL     string
	Format  string // "tar.gz" or "zip"
	Headers map[string]string
}

var binarySources = map[string]BinarySource{
	"linux-amd64": {
		URL:    "https://github.com/dscripka/whisper.cpp_binaries/releases/download/commit_3d42463/whisper-bin-linux-x64.tar.gz",
		Format: "tar.gz",
	},
	"darwin-arm64": {
		URL:     "https://ghcr.io/v2/homebrew/core/whisper-cpp/blobs/sha256:f0901568c7babbd3022a043887007400e4b57a22d3a90b9c0824d01fa3a77270",
		Format:  "tar.gz",
		Headers: map[string]string{"Authorization": "Bearer QQ=="},
	},
	"darwin-amd64": {
		URL:     "https://ghcr.io/v2/homebrew/core/whisper-cpp/blobs/sha256:e6c2f78cbc5d6b311dfe24d8c5d4ffc68a634465c5e35ed11746068583d273c4",
		Format:  "tar.gz",
		Headers: map[string]string{"Authorization": "Bearer QQ=="},
	},
	"linux-arm64": {
		URL:     "https://ghcr.io/v2/homebrew/core/whisper-cpp/blobs/sha256:684199fd6bec28cddfa086c584a49d236386c109f901a443b577b857fd052f83",
		Format:  "tar.gz",
		Headers: map[string]string{"Authorization": "Bearer QQ=="},
	},
	"windows-amd64": {
		URL:    "https://github.com/ggml-org/whisper.cpp/releases/download/v1.7.6/whisper-bin-x64.zip",
		Format: "zip",
	},
}

// mimeTypes maps audio file extensions to MIME types.
var mimeTypes = map[string]string{
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".wav":  "audio/wav",
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".webm": "audio/webm",
}

// ---------------------------------------------------------------------------
// Global state
// ---------------------------------------------------------------------------

var (
	warmupOnce   sync.Once
	warmupErr    error
	whisperDir   string // .claude/claudeclaw/whisper
	transcribeMu sync.Mutex
)

func init() {
	whisperDir = filepath.Join(config.BaseDir(), "whisper")
}

func binDir() string    { return filepath.Join(whisperDir, "bin") }
func libDir() string    { return filepath.Join(whisperDir, "lib") }
func modelDir() string  { return filepath.Join(whisperDir, "models") }
func tmpDir() string    { return filepath.Join(whisperDir, "tmp") }

func whisperBinaryPath() string {
	name := "whisper-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(binDir(), name)
}

func modelPath() string {
	return filepath.Join(modelDir(), fmt.Sprintf("ggml-%s.bin", whisperModel))
}

// debugLog is a helper that only logs when debug is true.
type debugLog struct {
	enabled bool
}

func (d debugLog) log(format string, args ...interface{}) {
	if d.enabled {
		log.Printf(format, args...)
	}
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// WarmupWhisperAssets ensures the whisper binary and model are downloaded and
// ready to use. Safe to call multiple times; work is performed only once.
func WarmupWhisperAssets() error {
	warmupOnce.Do(func() {
		warmupErr = prepareWhisperAssets()
	})
	return warmupErr
}

// TranscribeAudioToText transcribes the audio file at inputPath to text.
// If the STT API is configured in settings it is used; otherwise the local
// whisper-cli binary is invoked.
func TranscribeAudioToText(inputPath string, debug bool) (string, error) {
	transcribeMu.Lock()
	defer transcribeMu.Unlock()

	dl := debugLog{enabled: debug}

	settings := config.GetSettings()
	if settings.STT.BaseURL != "" {
		return transcribeViaAPI(inputPath, settings.STT.BaseURL, settings.STT.Model, dl)
	}

	// Local whisper path
	if err := WarmupWhisperAssets(); err != nil {
		return "", fmt.Errorf("whisper warmup failed: %w", err)
	}
	dl.log("whisper: warmup ready, input=%s", inputPath)

	if info, err := os.Stat(inputPath); err == nil {
		dl.log("whisper: input size=%d bytes", info.Size())
	}

	wavPath, err := ensureWavInput(inputPath, dl)
	if err != nil {
		return "", err
	}
	shouldCleanup := wavPath != inputPath
	defer func() {
		if shouldCleanup {
			dl.log("whisper: cleanup wav=%s", wavPath)
			os.Remove(wavPath)
		}
	}()

	dl.log("whisper: using wav=%s cleanup=%v", wavPath, shouldCleanup)

	transcript, err := runWhisperCLI(wavPath, dl)
	if err != nil {
		// If the binary is missing, try re-downloading.
		if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") {
			dl.log("whisper: binary missing, re-downloading")
			os.RemoveAll(binDir())
			// Reset sync.Once so warmup runs again.
			warmupOnce = sync.Once{}
			warmupErr = nil
			if warmupErr2 := WarmupWhisperAssets(); warmupErr2 != nil {
				return "", fmt.Errorf("whisper re-warmup failed: %w", warmupErr2)
			}
			transcript, err = runWhisperCLI(wavPath, dl)
			if err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}

	dl.log("whisper: transcript chars=%d", len(transcript))
	return transcript, nil
}

// ---------------------------------------------------------------------------
// STT API transcription
// ---------------------------------------------------------------------------

func transcribeViaAPI(inputPath, baseURL, model string, dl debugLog) (string, error) {
	if model == "" {
		model = defaultSTTModel
	}
	apiURL := strings.TrimRight(baseURL, "/") + "/v1/audio/transcriptions"
	dl.log("whisper: using STT API url=%s model=%s", apiURL, model)

	audioData, err := os.ReadFile(inputPath)
	if err != nil {
		return "", fmt.Errorf("whisper: read audio file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(inputPath))
	if ext == "" {
		ext = ".ogg"
	}
	mimeType, ok := mimeTypes[ext]
	if !ok {
		mimeType = "audio/ogg"
	}
	_ = mimeType // used implicitly via Content-Type in the multipart part

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", "audio"+ext)
	if err != nil {
		return "", fmt.Errorf("whisper: create form file: %w", err)
	}
	if _, err := part.Write(audioData); err != nil {
		return "", fmt.Errorf("whisper: write audio data: %w", err)
	}
	if err := writer.WriteField("model", model); err != nil {
		return "", fmt.Errorf("whisper: write model field: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("whisper: close multipart writer: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, apiURL, &body)
	if err != nil {
		return "", fmt.Errorf("whisper: create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper: STT API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("whisper: read STT API response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("whisper: STT API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("whisper: parse STT API response: %w", err)
	}

	transcript := strings.TrimSpace(result.Text)
	dl.log("whisper: API transcript chars=%d", len(transcript))
	return transcript, nil
}

// ---------------------------------------------------------------------------
// Local whisper binary execution
// ---------------------------------------------------------------------------

func runWhisperCLI(wavPath string, dl debugLog) (string, error) {
	binPath := whisperBinaryPath()
	mdlPath := modelPath()

	ctx, cancel := context.WithTimeout(context.Background(), whisperTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "-m", mdlPath, "-f", wavPath, "--no-timestamps")

	// Set library paths so whisper can find shared libs.
	env := os.Environ()
	lib := libDir()
	env = appendLibPath(env, "LD_LIBRARY_PATH", lib)
	env = appendLibPath(env, "DYLD_LIBRARY_PATH", lib)
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("whisper: transcription timed out after %s", whisperTimeout)
		}
		return "", fmt.Errorf("whisper: transcription failed: %w: %s", err, stderr.String())
	}

	// Parse output: strip blank lines and [BLANK_AUDIO], join with spaces.
	var parts []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "[BLANK_AUDIO]" {
			continue
		}
		parts = append(parts, trimmed)
	}
	transcript := strings.Join(parts, " ")
	// Collapse multiple spaces.
	for strings.Contains(transcript, "  ") {
		transcript = strings.ReplaceAll(transcript, "  ", " ")
	}
	return strings.TrimSpace(transcript), nil
}

// appendLibPath prepends a directory to a PATH-style environment variable.
func appendLibPath(env []string, key, dir string) []string {
	for i, e := range env {
		if strings.HasPrefix(e, key+"=") {
			existing := e[len(key)+1:]
			env[i] = key + "=" + dir + ":" + existing
			return env
		}
	}
	return append(env, key+"="+dir)
}

// ---------------------------------------------------------------------------
// Audio format conversion
// ---------------------------------------------------------------------------

func ensureWavInput(inputPath string, dl debugLog) (string, error) {
	ext := strings.ToLower(filepath.Ext(inputPath))
	dl.log("whisper: input path=%s ext=%s", inputPath, ext)

	if ext == ".wav" {
		return inputPath, nil
	}

	if ext != ".ogg" && ext != ".oga" {
		return "", fmt.Errorf("whisper: unsupported audio format %q; supported: .ogg, .oga, .wav", ext)
	}

	if err := os.MkdirAll(tmpDir(), 0o755); err != nil {
		return "", fmt.Errorf("whisper: create tmp dir: %w", err)
	}

	base := strings.TrimSuffix(filepath.Base(inputPath), ext)
	wavPath := filepath.Join(tmpDir(), fmt.Sprintf("%s-%d.wav", base, time.Now().UnixMilli()))

	// Try ffmpeg first.
	if ffmpegPath, err := exec.LookPath("ffmpeg"); err == nil {
		dl.log("whisper: converting via ffmpeg")
		cmd := exec.Command(ffmpegPath, "-i", inputPath, "-ar", "16000", "-ac", "1", "-y", wavPath)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			dl.log("whisper: ffmpeg conversion failed: %s", stderr.String())
			// Fall through to try using the ogg file directly.
		} else {
			return wavPath, nil
		}
	}

	// If ffmpeg is not available, try using the ogg file directly with whisper.
	dl.log("whisper: ffmpeg not available, using ogg file directly")
	return inputPath, nil
}

// ---------------------------------------------------------------------------
// Asset preparation (binary + model download)
// ---------------------------------------------------------------------------

func prepareWhisperAssets() error {
	start := time.Now()
	log.Printf("whisper warmup: start root=%s model=%s", whisperDir, whisperModel)

	for _, dir := range []string{whisperDir, tmpDir(), binDir(), libDir(), modelDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("whisper: mkdir %s: %w", dir, err)
		}
	}

	binPath := whisperBinaryPath()
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		if err := downloadAndExtractBinary(); err != nil {
			return err
		}
	} else {
		log.Println("whisper warmup: binary exists")
	}

	if err := downloadModel(); err != nil {
		return err
	}

	log.Printf("whisper warmup: complete in %dms", time.Since(start).Milliseconds())
	return nil
}

func downloadAndExtractBinary() error {
	platformKey := runtime.GOOS + "-" + runtime.GOARCH
	source, ok := binarySources[platformKey]
	if !ok {
		keys := make([]string, 0, len(binarySources))
		for k := range binarySources {
			keys = append(keys, k)
		}
		return fmt.Errorf("whisper: no pre-built binary for %s; supported: %s", platformKey, strings.Join(keys, ", "))
	}

	extractDir := filepath.Join(tmpDir(), "extract")
	os.RemoveAll(extractDir)
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return fmt.Errorf("whisper: mkdir extract: %w", err)
	}

	archiveExt := "tar.gz"
	if source.Format == "zip" {
		archiveExt = "zip"
	}
	archivePath := filepath.Join(tmpDir(), "whisper-bin."+archiveExt)

	log.Printf("whisper: downloading binary for %s...", platformKey)
	if err := downloadFile(source.URL, archivePath, source.Headers); err != nil {
		return fmt.Errorf("whisper: download binary: %w", err)
	}

	log.Println("whisper: extracting...")
	if source.Format == "tar.gz" {
		cmd := exec.Command("tar", "xzf", archivePath, "-C", extractDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("whisper: extract tar.gz: %w: %s", err, string(out))
		}
	} else {
		cmd := exec.Command("unzip", "-o", archivePath, "-d", extractDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("whisper: extract zip: %w: %s", err, string(out))
		}
	}

	// Find the whisper binary (could be named whisper-cli or main).
	found, err := findExecutable(extractDir, []string{"whisper-cli", "main"})
	if err != nil || found == "" {
		return fmt.Errorf("whisper: could not find whisper-cli or main binary in archive")
	}

	destBinary := whisperBinaryPath()
	if err := copyFile(found, destBinary); err != nil {
		return fmt.Errorf("whisper: copy binary: %w", err)
	}
	if err := os.Chmod(destBinary, 0o755); err != nil {
		return fmt.Errorf("whisper: chmod binary: %w", err)
	}

	// Copy shared libraries (for Homebrew bottles).
	if err := copySharedLibs(extractDir, libDir()); err != nil {
		log.Printf("whisper: warning copying shared libs: %v", err)
	}

	// Cleanup.
	os.RemoveAll(extractDir)
	os.Remove(archivePath)
	log.Println("whisper: binary ready")
	return nil
}

func downloadModel() error {
	mp := modelPath()
	if _, err := os.Stat(mp); err == nil {
		return nil
	}

	if err := os.MkdirAll(modelDir(), 0o755); err != nil {
		return fmt.Errorf("whisper: mkdir models: %w", err)
	}

	log.Printf("whisper: downloading model %s...", whisperModel)
	if err := downloadFile(modelURL, mp, nil); err != nil {
		return fmt.Errorf("whisper: download model: %w", err)
	}
	log.Println("whisper: model ready")
	return nil
}

// ---------------------------------------------------------------------------
// File download with resume support
// ---------------------------------------------------------------------------

func downloadFile(url, destPath string, headers map[string]string) error {
	tmpPath := destPath + ".tmp"
	var existingBytes int64

	if info, err := os.Stat(tmpPath); err == nil {
		existingBytes = info.Size()
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if existingBytes > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingBytes))
		log.Printf("whisper: resuming download from %s", formatBytes(existingBytes))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	isResume := resp.StatusCode == http.StatusPartialContent && existingBytes > 0
	if !isResume && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed (%d): %s", resp.StatusCode, url)
	}

	// If server ignored Range and sent the full file, start over.
	if existingBytes > 0 && resp.StatusCode == http.StatusOK {
		existingBytes = 0
		os.Remove(tmpPath)
	}

	contentLength := resp.ContentLength
	var totalSize int64
	if isResume {
		totalSize = existingBytes + contentLength
	} else if contentLength > 0 {
		totalSize = contentLength
	}

	// Open file for writing (append if resuming).
	flags := os.O_CREATE | os.O_WRONLY
	if isResume {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(tmpPath, flags, 0o644)
	if err != nil {
		return fmt.Errorf("open tmp file: %w", err)
	}

	received := existingBytes
	lastLog := time.Now()
	buf := make([]byte, 64*1024)

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				f.Close()
				return fmt.Errorf("write to tmp file: %w", writeErr)
			}
			received += int64(n)
			if totalSize > 0 && time.Since(lastLog) > 2*time.Second {
				pct := int(float64(received) / float64(totalSize) * 100)
				log.Printf("whisper: downloading %s / %s (%d%%)", formatBytes(received), formatBytes(totalSize), pct)
				lastLog = time.Now()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			f.Close()
			return fmt.Errorf("read response body: %w", readErr)
		}
	}
	f.Close()

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("rename tmp to dest: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func formatBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%dKB", b/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	}
}

// findExecutable recursively searches dir for a file with one of the given
// names and returns its full path.
func findExecutable(dir string, names []string) (string, error) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}

	targets := make(map[string]bool)
	for _, name := range names {
		targets[name] = true
		if suffix != "" {
			targets[name+suffix] = true
		}
	}

	var found string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if !info.IsDir() && targets[info.Name()] {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// copySharedLibs copies whisper shared libraries from extractDir into libDir.
func copySharedLibs(extractDir, destLib string) error {
	return filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		isWhisperLib := strings.Contains(name, "whisper") &&
			(strings.HasSuffix(name, ".so") || strings.HasSuffix(name, ".dylib") || strings.Contains(name, ".so."))
		if isWhisperLib {
			dest := filepath.Join(destLib, name)
			if cpErr := copyFile(path, dest); cpErr != nil {
				return fmt.Errorf("copy lib %s: %w", name, cpErr)
			}
		}
		return nil
	})
}
