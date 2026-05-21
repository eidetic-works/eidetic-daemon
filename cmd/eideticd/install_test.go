package main

import (
	"encoding/xml"
	"strings"
	"testing"
)

// plistDict matches the shape we render. Top-level <string> + nested
// <array><string> (for ProgramArguments) both contribute to AllStrings.
type plistDict struct {
	XMLName    xml.Name `xml:"plist"`
	Version    string   `xml:"version,attr"`
	AllStrings []string `xml:"dict>string"`
	ArrayStrs  []string `xml:"dict>array>string"`
}

// allStrings returns the flat list of every <string> in the plist (top-level
// + array-nested).
func (p plistDict) allStrings() []string {
	out := make([]string, 0, len(p.AllStrings)+len(p.ArrayStrs))
	out = append(out, p.AllStrings...)
	out = append(out, p.ArrayStrs...)
	return out
}

// TestRenderLaunchdPlistVanilla — sanity round-trip on a normal path.
func TestRenderLaunchdPlistVanilla(t *testing.T) {
	out, err := renderLaunchdPlist("works.eidetic.eideticd", "/usr/local/bin/eideticd")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(out), "/usr/local/bin/eideticd") {
		t.Errorf("plist should contain the exe path; got: %s", out)
	}
	// XML round-trip — confirms well-formed output.
	var parsed plistDict
	if err := xml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("plist not valid XML: %v\n%s", err, out)
	}
}

// TestRenderLaunchdPlistEscapesAmpersand is the headline regression for the
// CRITICAL audit finding (`cmd/eideticd/install.go:53-73`): a literal `&`
// in the executable path made the plist malformed XML, which either crashed
// launchctl bootstrap OR silently mis-registered the service. Post-fix,
// `&` is escaped to `&amp;` via encoding/xml.EscapeText, so the plist
// stays well-formed and launchctl reads back the original path.
func TestRenderLaunchdPlistEscapesAmpersand(t *testing.T) {
	exePath := "/Users/p&q/bin/eideticd"
	out, err := renderLaunchdPlist("works.eidetic.eideticd", exePath)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Raw output must NOT contain the un-escaped & in the string content
	// (an un-escaped & in XML content area is malformed).
	if strings.Contains(string(out), "<string>"+exePath+"</string>") {
		t.Fatalf("plist contains un-escaped %q in <string> body — malformed XML:\n%s",
			exePath, out)
	}
	if !strings.Contains(string(out), "&amp;") {
		t.Errorf("plist should contain &amp; escape; got:\n%s", out)
	}

	// Round-trip via encoding/xml. Decoder unescapes &amp; back to & so the
	// recovered string equals the original exe path.
	var parsed plistDict
	if err := xml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("plist with ampersand not valid XML: %v\n%s", err, out)
	}
	var found bool
	all := parsed.allStrings()
	for _, s := range all {
		if s == exePath {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("decoded plist strings do not include original exe path %q; got %v",
			exePath, all)
	}
}

// TestRenderLaunchdPlistEscapesAllMetacharacters — covers <, >, &, ', "
// (XML's five predefined entities).
func TestRenderLaunchdPlistEscapesAllMetacharacters(t *testing.T) {
	exePath := `/Users/<weird>&"path"'s/bin/eideticd`
	out, err := renderLaunchdPlist("works.eidetic.eideticd", exePath)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Plist must still parse as XML.
	var parsed plistDict
	if err := xml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("plist with metacharacters not valid XML: %v\n%s", err, out)
	}
	var found bool
	all := parsed.allStrings()
	for _, s := range all {
		if s == exePath {
			found = true
		}
	}
	if !found {
		t.Errorf("decoded plist strings do not include original metacharacter exe path %q; got %v",
			exePath, all)
	}
}

// TestRenderLaunchdPlistEscapesLabel — a label containing metacharacters
// (unlikely but possible if someone customizes launchdLabel) also stays
// well-formed.
func TestRenderLaunchdPlistEscapesLabel(t *testing.T) {
	out, err := renderLaunchdPlist("works.eidetic.<bad>", "/usr/local/bin/eideticd")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var parsed plistDict
	if err := xml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("plist with metacharacter label not valid XML: %v\n%s", err, out)
	}
}
