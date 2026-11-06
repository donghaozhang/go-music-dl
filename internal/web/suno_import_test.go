package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const testSunoID = "630dc14b-93b8-4c15-8848-123972d56375"

func TestSunoURLBoundary(t *testing.T) {
	for _, raw := range []string{"http://suno.com/song/" + testSunoID, "https://suno.com.evil.test/song/" + testSunoID, "https://suno.com@127.0.0.1/song/" + testSunoID, "https://suno.com:444/song/" + testSunoID, "https://suno.com/playlist/" + testSunoID, "file:///etc/passwd", "https://suno.com/song/../../etc"} {
		if _, _, err := normalizeSunoURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	canonical, id, err := normalizeSunoURL("https://www.suno.com/song/" + testSunoID + "?utm=abc#x")
	if err != nil || canonical != "https://suno.com/song/"+testSunoID || id != testSunoID {
		t.Fatal(canonical, id, err)
	}
	if _, id, err := normalizeSunoURL("https://suno.com/s/AbCD1234"); err != nil || id != "" {
		t.Fatal(id, err)
	}
	for _, raw := range []string{"https://127.0.0.1/a", "https://cdn1.suno.ai.evil/a", "https://cdn1.suno.ai:443/a", "https://user@cdn1.suno.ai/a", "http://cdn1.suno.ai/a"} {
		if allowedSunoMediaURL(raw) {
			t.Fatal(raw)
		}
	}
}

func TestSunoFlightChineseAcrossChunks(t *testing.T) {
	lyrics := "[Verse 1]\n这是测试歌词\na:123\n第二行包含中文"
	clip := map[string]any{"id": testSunoID, "status": "complete", "title": "测试", "audio_url": "https://cdn1.suno.ai/test.mp3", "metadata": map[string]any{"prompt": "$a", "duration": 215}}
	encoded, _ := json.Marshal(map[string]any{"state": map[string]any{"data": clip}})
	payload := fmt.Sprintf("a:T%x,%s\nb:%s\n", len([]byte(lyrics)), lyrics, encoded)
	var page strings.Builder
	for _, chunk := range []string{string([]rune(payload)[:23]), string([]rune(payload)[23:])} {
		e, _ := json.Marshal([]any{1, chunk})
		fmt.Fprintf(&page, "<script>self.__next_f.push(%s)</script>", e)
	}
	got, err := parseSunoPage([]byte(page.String()), testSunoID)
	if err != nil || got.Metadata.Prompt != lyrics || got.ID != testSunoID {
		t.Fatalf("%+v %v", got, err)
	}
	// A recommended song before the canonical tag must not select the wrong clip.
	shared := `<a href="https://suno.com/song/00000000-0000-0000-0000-000000000000">Other</a><meta content="https://suno.com/song/` + testSunoID + `" property="og:url">` + page.String()
	if got, err := parseSunoPage([]byte(shared), ""); err != nil || got.ID != testSunoID {
		t.Fatal("share identity", got.ID, err)
	}
	if _, err := parseSunoPage([]byte(page.String()), ""); err == nil {
		t.Fatal("share accepted without canonical identity")
	}
	if _, err := parseSunoPage([]byte(page.String()), "00000000-0000-0000-0000-000000000000"); err == nil {
		t.Fatal("used a different song")
	}
}

func TestSunoMissingVolumeAndExistingFile(t *testing.T) {
	root := t.TempDir()
	if _, err := importSunoSong(context.Background(), "", testSunoID, filepath.Join(root, "missing")); err == nil {
		t.Fatal("created unavailable download path")
	}
	filename := "Suno_" + testSunoID + ".mp3"
	if err := os.WriteFile(filepath.Join(root, filename), []byte("existing-file-do-not-replace"), 0600); err != nil {
		t.Fatal(err)
	}
	lrc := "[00:01.00]已有同步歌词\n"
	os.WriteFile(filepath.Join(root, "Suno_"+testSunoID+".lrc"), []byte(lrc), 0600)
	got, err := importSunoSong(context.Background(), "", testSunoID, root)
	if err != nil || !got.Existing || got.LyricKind != "synced" {
		t.Fatalf("%+v %v", got, err)
	}
	b, _ := os.ReadFile(filepath.Join(root, filename))
	if string(b) != "existing-file-do-not-replace" {
		t.Fatal("overwrote file")
	}
	if _, err := readSunoLimited(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("ignored size limit")
	}
}

func TestSunoImportWriteGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSunoImportRoutes(r.Group("/music"))
	original := sunoImportRunner
	defer func() { sunoImportRunner = original }()
	called := false
	sunoImportRunner = func(context.Context, string, string, string) (*sunoImportResult, error) {
		called = true
		return nil, fmt.Errorf("expected test error")
	}
	for _, tc := range []struct {
		remote, origin, xhr string
		status              int
	}{
		{"203.0.113.1:1234", "http://music.test", "XMLHttpRequest", 403},
		{"127.0.0.1:1234", "https://evil.test", "XMLHttpRequest", 403},
		{"127.0.0.1:1234", "http://music.test", "", 403},
		{"127.0.0.1:1234", "http://music.test", "XMLHttpRequest", 502},
	} {
		body, _ := json.Marshal(map[string]string{"url": "https://suno.com/song/" + testSunoID})
		req := httptest.NewRequest(http.MethodPost, "http://music.test/music/suno/import", bytes.NewReader(body))
		req.RemoteAddr = tc.remote
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("X-Requested-With", tc.xhr)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
	if !called {
		t.Fatal("same-origin local request did not reach importer")
	}
}

// Opt-in: user-owned/public clip only, writes all media under the given existing
// external-volume directory. Disabled in ordinary tests and CI.
func TestSunoLiveImport(t *testing.T) {
	link := os.Getenv("MUSIC_DL_SUNO_TEST_URL")
	dir := os.Getenv("MUSIC_DL_SUNO_TEST_DIR")
	if link == "" || dir == "" {
		t.Skip("opt-in live test")
	}
	if project := os.Getenv("MUSIC_DL_SUNO_TEST_PROJECT"); project != "" {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(project); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(cwd)
	}
	canonical, id, err := normalizeSunoURL(link)
	if err != nil {
		t.Fatal(err)
	}
	got, err := importSunoSong(context.Background(), canonical, id, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.LyricKind == "none" {
		t.Fatal("expected fixture lyrics")
	}
	again, err := importSunoSong(context.Background(), canonical, id, dir)
	if err != nil || !again.Existing || again.Path != got.Path {
		t.Fatal("duplicate import", err)
	}
	report, _ := json.MarshalIndent(got, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "live-check.json"), report, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("Imported and verified:", got.Path, "lyrics:", got.LyricKind)
}
