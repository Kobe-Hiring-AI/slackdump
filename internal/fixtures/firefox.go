package fixtures

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// FirefoxSlackDCookie reads the Slack "d" cookie from the default Firefox
// profile. Calls t.Skip if Firefox is not available or the cookie is missing.
func FirefoxSlackDCookie(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Firefox cookie extraction only supported on Linux")
	}

	dir, err := firefoxDir()
	if err != nil {
		t.Skipf("Firefox not available: %v", err)
	}

	profile, err := parseProfilesINI(filepath.Join(dir, "profiles.ini"))
	if err != nil {
		t.Skipf("Firefox profiles.ini not found or unreadable: %v", err)
	}

	cookiePath := filepath.Join(dir, profile, "cookies.sqlite")
	if _, err := os.Stat(cookiePath); err != nil {
		t.Skipf("Firefox cookies.sqlite not found: %v", err)
	}

	val, err := readSlackDCookie(cookiePath)
	if err != nil {
		t.Fatalf("reading Slack d cookie: %v", err)
	}
	if val == "" {
		t.Skip("Slack d cookie not found in Firefox")
	}
	return val
}

func firefoxDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".mozilla", "firefox")
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// parseProfilesINI reads profiles.ini and returns the relative path of the
// default profile. It first looks for an [Install*] section with a Default
// key (Firefox 67+), then falls back to a [Profile*] section with Default=1.
func parseProfilesINI(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var (
		installDefault string
		profileDefault string
		currentSection string
		currentPath    string
		currentIsDefault bool
	)

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			// Flush previous profile section.
			if strings.HasPrefix(currentSection, "Profile") && currentIsDefault && currentPath != "" {
				profileDefault = currentPath
			}
			currentSection = strings.Trim(line, "[]")
			currentPath = ""
			currentIsDefault = false
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)

		switch {
		case strings.HasPrefix(currentSection, "Install") && k == "Default":
			installDefault = v
		case strings.HasPrefix(currentSection, "Profile") && k == "Path":
			currentPath = v
		case strings.HasPrefix(currentSection, "Profile") && k == "Default" && v == "1":
			currentIsDefault = true
		}
	}
	// Flush last section.
	if strings.HasPrefix(currentSection, "Profile") && currentIsDefault && currentPath != "" {
		profileDefault = currentPath
	}

	if installDefault != "" {
		return installDefault, nil
	}
	if profileDefault != "" {
		return profileDefault, nil
	}
	return "", fmt.Errorf("no default profile found in %s", path)
}

func readSlackDCookie(dbPath string) (string, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&immutable=1")
	if err != nil {
		return "", err
	}
	defer db.Close()

	var value string
	err = db.QueryRow("SELECT value FROM moz_cookies WHERE name = 'd' AND host = '.slack.com' LIMIT 1").Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}
