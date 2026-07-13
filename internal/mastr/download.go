package mastr

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

// DatendownloadPageURL is the public page listing the current
// Gesamtdatenexport download link.
const DatendownloadPageURL = "https://www.marktstammdatenregister.de/MaStR/Datendownload"

// gesamtdatenexportPattern matches the current Gesamtdatenexport download
// link embedded in the Datendownload page HTML, e.g.
// "https://download.marktstammdatenregister.de/Gesamtdatenexport_20260713_26.1.zip".
var gesamtdatenexportPattern = regexp.MustCompile(`https://download\.marktstammdatenregister\.de/Gesamtdatenexport_\d{8}_[0-9.]+\.zip`)

// DiscoverLatestDownloadURL fetches the public Datendownload page and
// extracts the current Gesamtdatenexport zip URL. The link changes with
// every new export (date + version encoded in the file name), so it must be
// (re-)discovered rather than hardcoded.
func DiscoverLatestDownloadURL(userAgent string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, DatendownloadPageURL, nil)
	if err != nil {
		return "", fmt.Errorf("mastr: building request: %w", err)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("mastr: fetching %s: %w", DatendownloadPageURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mastr: unexpected status %d from %s", resp.StatusCode, DatendownloadPageURL)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("mastr: reading response body: %w", err)
	}
	m := gesamtdatenexportPattern.Find(body)
	if m == nil {
		return "", fmt.Errorf("mastr: no Gesamtdatenexport link found on %s", DatendownloadPageURL)
	}
	return string(m), nil
}

// DownloadZip streams the file at url into destPath, creating parent
// directories as needed. Any existing file at destPath is overwritten.
func DownloadZip(url, destPath, userAgent string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("mastr: creating directory for %s: %w", destPath, err)
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("mastr: building request: %w", err)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("mastr: downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mastr: unexpected status %d downloading %s", resp.StatusCode, url)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("mastr: creating %s: %w", destPath, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("mastr: writing %s: %w", destPath, err)
	}
	return nil
}
