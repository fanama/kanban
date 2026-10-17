package gsheets

import "testing"

func TestParseSpreadsheetURL(t *testing.T) {
	for _, tc := range urlCases {
		t.Run(tc.name, func(t *testing.T) {
			id, gid, err := ParseSpreadsheetURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Expected an error, got id=%q gid=%v", id, gid)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSpreadsheetURL failed: %v", err)
			}
			if id != tc.wantID {
				t.Errorf("Expected id %q, got %q", tc.wantID, id)
			}
			if !sameGID(gid, tc.wantGID) {
				t.Errorf("Expected gid %v, got %v", tc.wantGID, gid)
			}
		})
	}
}

func sameGID(got, want *int64) bool {
	switch {
	case want == nil:
		return got == nil
	case got == nil:
		return false
	default:
		return *got == *want
	}
}

// urlCases lists the links and ids ParseSpreadsheetURL must read.
var urlCases = []struct {
	name    string
	in      string
	wantID  string
	wantGID *int64
	wantErr bool
}{
	{
		name:    "edit link with gid fragment",
		in:      "https://docs.google.com/spreadsheets/d/1Jll86VJaRlwOXFOocMihjag4bM5li1Mu-rmLObXeQCc/edit?usp=sharing#gid=0",
		wantID:  "1Jll86VJaRlwOXFOocMihjag4bM5li1Mu-rmLObXeQCc",
		wantGID: ptr(int64(0)),
	},
	{
		name:    "link without gid",
		in:      "https://docs.google.com/spreadsheets/d/abc-123/edit",
		wantID:  "abc-123",
		wantGID: nil,
	},
	{
		name:    "gid as query parameter",
		in:      "https://docs.google.com/spreadsheets/d/abc-123/edit?gid=42",
		wantID:  "abc-123",
		wantGID: ptr(int64(42)),
	},
	{
		name:   "bare id",
		in:     "1Jll86VJaRlwOXFOocMihjag4bM5li1Mu-rmLObXeQCc",
		wantID: "1Jll86VJaRlwOXFOocMihjag4bM5li1Mu-rmLObXeQCc",
	},
	{name: "no id", in: "https://docs.google.com/spreadsheets/", wantErr: true},
	{name: "empty", in: "   ", wantErr: true},
}

func TestA1NameQuotesNamesWithSpaces(t *testing.T) {
	if got := a1Name("Sheet1"); got != "Sheet1" {
		t.Errorf("Plain name should stay unquoted, got %q", got)
	}
	if got := a1Name("My Sheet"); got != "'My Sheet'" {
		t.Errorf("Name with a space should be quoted, got %q", got)
	}
}

func ptr[T any](v T) *T { return &v }
