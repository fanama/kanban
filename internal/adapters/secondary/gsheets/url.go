package gsheets

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParseSpreadsheetURL extracts the spreadsheet ID and the gid from a Google
// Sheets link such as:
//
//	https://docs.google.com/spreadsheets/d/<spreadsheet-id>/edit?usp=sharing#gid=0
//
// The returned gid is nil when the URL carries no gid.
func ParseSpreadsheetURL(raw string) (spreadsheetID string, gid *int64, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil, fmt.Errorf("gsheets: empty spreadsheet URL")
	}

	// The ID may also be passed bare (no scheme, no slashes).
	if !strings.Contains(raw, "/") {
		return raw, nil, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", nil, fmt.Errorf("gsheets: parse spreadsheet URL: %w", err)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "d" && i+1 < len(parts) {
			spreadsheetID = parts[i+1]
			break
		}
	}
	if spreadsheetID == "" {
		return "", nil, fmt.Errorf("gsheets: no spreadsheet ID in URL %q", raw)
	}

	// The gid is usually the URL fragment, sometimes the "gid" query value.
	rawGID := u.Fragment
	if rawGID == "" {
		rawGID = u.Query().Get("gid")
	}
	rawGID = strings.TrimPrefix(rawGID, "gid=")
	if rawGID == "" {
		return spreadsheetID, nil, nil
	}

	v, err := strconv.ParseInt(rawGID, 10, 64)
	if err != nil {
		return "", nil, fmt.Errorf("gsheets: invalid gid %q in URL: %w", rawGID, err)
	}
	return spreadsheetID, &v, nil
}
