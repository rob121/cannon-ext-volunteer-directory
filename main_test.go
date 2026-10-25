package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rob121/cannon/extension"
)

func TestCapabilities(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/capabilities", nil)
	rec := httptest.NewRecorder()
	newServer().Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Capabilities map[string]string `json:"capabilities"`
		Defaults     struct {
			Admin struct {
				MenuName string `json:"menu_name"`
			} `json:"admin"`
		} `json:"defaults"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"page":          "/page",
		"data":          "/data",
		"admin":         "/admin",
		"help":          "/help",
		"templates":     "/templates",
		"notifications": "/notifications",
	}
	for key, path := range want {
		if got.Capabilities[key] != path {
			t.Fatalf("capability %s = %q, want %q", key, got.Capabilities[key], path)
		}
	}
	if _, ok := got.Capabilities["block"]; ok {
		t.Fatal("block capability should stay unset")
	}
	if got.Defaults.Admin.MenuName != defaultAdminMenu {
		t.Fatalf("menu name %q", got.Defaults.Admin.MenuName)
	}
}

func TestMeta(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/meta", nil)
	rec := httptest.NewRecorder()
	newServer().Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var got struct {
		Name          string `json:"name"`
		Title         string `json:"title"`
		Version       string `json:"version"`
		UpdateURLBase string `json:"update_url_base"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != extensionName || got.Title != extensionTitle || got.Version != extensionVersion || got.UpdateURLBase != updateURLBase {
		t.Fatalf("meta = %#v", got)
	}
}

func TestTemplates(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/templates", nil)
	rec := httptest.NewRecorder()
	newServer().Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got extension.TemplateListResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"volunteer/form.html":         false,
		"volunteer/admin_list.html":   false,
		"volunteer/admin_entry.html":  false,
		"volunteer/admin_skills.html": false,
	}
	for _, tmpl := range got.Templates {
		if _, ok := want[tmpl.Path]; ok {
			want[tmpl.Path] = true
		}
	}
	for path, seen := range want {
		if !seen {
			t.Fatalf("missing template %s in %#v", path, got.Templates)
		}
	}
}

func TestNotificationsEvent(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	rec := httptest.NewRecorder()
	newServer().Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got extension.NotificationEventListResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 || got.Events[0].Event != volunteerEvent {
		t.Fatalf("events = %#v", got.Events)
	}
	keys := map[string]bool{}
	for _, placeholder := range got.Events[0].Placeholders {
		keys[placeholder.Key] = true
	}
	for _, key := range []string{"name", "email", "phone", "address", "skills", "other_skill", "note", "admin_url", "site_id"} {
		if !keys[key] {
			t.Fatalf("missing placeholder %s", key)
		}
	}
}

func TestInstallSeedsSkills(t *testing.T) {
	setupExtensionDB(t)
	server := newServer().Handler()
	resp := postWire(t, server, "/install", extension.WireRequest{Method: http.MethodPost, URL: "/install", SiteID: "example"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("install status %d: %s", resp.StatusCode, resp.Body)
	}
	db, _, err := extension.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"volunteer_directory_settings", "volunteer_directory_entries"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("missing table %s", table)
		}
	}
	data, err := extension.DBConfigurationStore(db, "volunteer_directory_settings").Load(skillsSection)
	if err != nil {
		t.Fatal(err)
	}
	got := parseSkillList(data["skills"])
	want := defaultSkillList()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("skills = %#v", got)
	}
}

func TestPageRendersSkillCheckboxes(t *testing.T) {
	setupExtensionDB(t)
	server := newServer().Handler()
	resp := postWire(t, server, "/page/volunteer-directory", extension.WireRequest{
		Method: http.MethodPost,
		URL:    "/volunteer?sent=1",
		CSRF:   "token",
		SiteID: "example",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, resp.Body)
	}
	for _, skill := range defaultSkillList() {
		needle := `name="skills" value="` + skill + `"`
		if !strings.Contains(resp.Body, needle) {
			t.Fatalf("missing checkbox %q in %s", skill, resp.Body)
		}
	}
	if !strings.Contains(resp.Body, `name="note"`) || !strings.Contains(resp.Body, "required") {
		t.Fatal("note field should be required")
	}
	if !strings.Contains(resp.Body, `name="other_skill"`) {
		t.Fatal("missing other skill field")
	}
	if !strings.Contains(resp.Body, `<captcha context="form" provider="any"></captcha>`) {
		t.Fatal("missing captcha placeholder")
	}
	if !strings.Contains(resp.Body, `name="_csrf" value="token"`) {
		t.Fatal("missing csrf field")
	}
	if !strings.Contains(resp.Body, "Your information was saved") {
		t.Fatal("sent=1 should show the thank-you message")
	}
	if !strings.Contains(resp.Body, "/ext/") || !strings.Contains(resp.Body, "/volunteer/submit") {
		t.Fatalf("form action = %s", resp.Body)
	}
}

func TestSubmitRequiresNoteAndSkill(t *testing.T) {
	setupExtensionDB(t)
	server := newServer().Handler()

	missingNote := postWire(t, server, "/data/volunteer/submit", extension.WireRequest{
		Method: http.MethodPost,
		URL:    "/volunteer",
		Body:   submissionBody(map[string]string{"note": ""}, "Kitchen"),
		SiteID: "example",
	})
	if missingNote.StatusCode != http.StatusBadRequest || !strings.Contains(missingNote.Body, "Note is required.") {
		t.Fatalf("missing note: %#v", missingNote)
	}

	missingSkill := postWire(t, server, "/data/volunteer/submit", extension.WireRequest{
		Method: http.MethodPost,
		URL:    "/volunteer",
		Body:   submissionBody(map[string]string{"note": "I can help on weekends."}, ""),
		SiteID: "example",
	})
	if missingSkill.StatusCode != http.StatusBadRequest || !strings.Contains(missingSkill.Body, "Select at least one skill") {
		t.Fatalf("missing skill: %#v", missingSkill)
	}
}

func TestSubmitStoresEntryAndEmits(t *testing.T) {
	setupExtensionDB(t)
	server := newServer().Handler()
	body := submissionBody(map[string]string{
		"name":        "Ada Lovelace",
		"address":     "1 Camp Road",
		"phone":       "555-0100",
		"email":       "ada@example.com",
		"other_skill": "Archery",
		"note":        "Available on Saturdays.",
		"return_url":  "/volunteer",
	}, "Kitchen", "Music")
	resp := postWire(t, server, "/data/volunteer/submit", extension.WireRequest{
		Method: http.MethodPost,
		URL:    "/volunteer",
		Body:   body,
		SiteID: "example",
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d: %s", resp.StatusCode, resp.Body)
	}
	if loc := headerValue(resp, "Location"); loc != "/volunteer?sent=1" {
		t.Fatalf("location %q", loc)
	}
	if len(resp.EmitEvents) != 1 || resp.EmitEvents[0].Event != volunteerEvent {
		t.Fatalf("emit = %#v", resp.EmitEvents)
	}
	args := resp.EmitEvents[0].Arguments
	if args["name"] != "Ada Lovelace" || args["email"] != "ada@example.com" || args["phone"] != "555-0100" {
		t.Fatalf("identity args %#v", args)
	}
	if args["skills"] != "Kitchen, Music" || args["other_skill"] != "Archery" || args["note"] != "Available on Saturdays." {
		t.Fatalf("detail args %#v", args)
	}
	if args["site_id"] != "example" || !strings.Contains(args["admin_url"].(string), "/entries/") {
		t.Fatalf("link args %#v", args)
	}

	db, _, err := extension.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	var row volunteerEntry
	if err := db.Table("volunteer_directory_entries").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Name != "Ada Lovelace" || row.OtherSkill != "Archery" || row.Note != "Available on Saturdays." {
		t.Fatalf("stored %#v", row)
	}
	if strings.Join(decodeSkills(row.SkillsJSON), ", ") != "Kitchen, Music" {
		t.Fatalf("stored skills %s", row.SkillsJSON)
	}
}

func TestSubmitAcceptsOtherSkillWithoutCheckbox(t *testing.T) {
	setupExtensionDB(t)
	server := newServer().Handler()
	resp := postWire(t, server, "/data/volunteer/submit", extension.WireRequest{
		Method: http.MethodPost,
		URL:    "/volunteer",
		Body: submissionBody(map[string]string{
			"other_skill": "Blacksmithing",
			"note":        "I can demonstrate at camp.",
		}),
		SiteID: "example",
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d: %s", resp.StatusCode, resp.Body)
	}
	if resp.EmitEvents[0].Arguments["skills"] != "" || resp.EmitEvents[0].Arguments["other_skill"] != "Blacksmithing" {
		t.Fatalf("emit %#v", resp.EmitEvents[0].Arguments)
	}
}

func TestAdminSearchDetailAndDelete(t *testing.T) {
	setupExtensionDB(t)
	server := newServer().Handler()
	postWire(t, server, "/data/volunteer/submit", extension.WireRequest{
		Method: http.MethodPost,
		URL:    "/volunteer",
		Body: submissionBody(map[string]string{
			"name": "Kitchen Pat", "email": "pat@example.com", "phone": "555-1000",
			"address": "2 Pine Lane", "note": "I cook breakfast.",
		}, "Kitchen"),
		SiteID: "example",
	})
	postWire(t, server, "/data/volunteer/submit", extension.WireRequest{
		Method: http.MethodPost,
		URL:    "/volunteer",
		Body: submissionBody(map[string]string{
			"name": "Music Sam", "email": "sam@example.com", "phone": "555-2000",
			"address": "9 Oak Street", "note": "I bring a guitar.", "other_skill": "Sound",
		}, "Music"),
		SiteID: "example",
	})

	denied := postWire(t, server, "/admin/", extension.WireRequest{
		Method: http.MethodGet,
		URL:    adminURL(""),
		SiteID: "example",
	})
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous admin status %d", denied.StatusCode)
	}

	list := postWire(t, server, "/admin/", adminWire(http.MethodGet, adminURL("?q=guitar")))
	if list.StatusCode != http.StatusOK {
		t.Fatalf("search status %d: %s", list.StatusCode, list.Body)
	}
	if !strings.Contains(list.Body, "Music Sam") || strings.Contains(list.Body, "Kitchen Pat") {
		t.Fatalf("search body %s", list.Body)
	}
	if !strings.Contains(list.Body, "1 person") {
		t.Fatalf("count text %s", list.Body)
	}

	skillSearch := postWire(t, server, "/admin/", adminWire(http.MethodGet, adminURL("?q=breakfast")))
	if !strings.Contains(skillSearch.Body, "Kitchen Pat") || strings.Contains(skillSearch.Body, "Music Sam") {
		t.Fatalf("note search %s", skillSearch.Body)
	}

	db, _, err := extension.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	var row volunteerEntry
	if err := db.Table("volunteer_directory_entries").Where("email = ?", "sam@example.com").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	detail := postWire(t, server, "/admin/", adminWire(http.MethodGet, adminURL("/entries/"+strconv.FormatUint(uint64(row.ID), 10))))
	if !strings.Contains(detail.Body, "9 Oak Street") || !strings.Contains(detail.Body, "Sound") || !strings.Contains(detail.Body, "I bring a guitar.") {
		t.Fatalf("detail %s", detail.Body)
	}

	deleted := postWire(t, server, "/admin/", adminWire(http.MethodPost, adminURL("/entries/"+strconv.FormatUint(uint64(row.ID), 10)+"/delete")))
	if deleted.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete status %d: %s", deleted.StatusCode, deleted.Body)
	}
	var left int64
	if err := db.Table("volunteer_directory_entries").Where("email = ?", "sam@example.com").Count(&left).Error; err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("row still present: %d", left)
	}
}

func TestSkillEditorUpdatesCheckboxes(t *testing.T) {
	setupExtensionDB(t)
	server := newServer().Handler()
	saved := postWire(t, server, "/admin/", adminWireWithBody(http.MethodPost, adminURL("/skills"), "skills="+url.QueryEscape("Trail work\nKitchen\n")))
	if saved.StatusCode != http.StatusSeeOther {
		t.Fatalf("save status %d: %s", saved.StatusCode, saved.Body)
	}
	page := postWire(t, server, "/page/volunteer-directory", extension.WireRequest{
		Method: http.MethodPost,
		URL:    "/volunteer",
		SiteID: "example",
	})
	if !strings.Contains(page.Body, `value="Trail work"`) || !strings.Contains(page.Body, `value="Kitchen"`) {
		t.Fatalf("updated skills missing: %s", page.Body)
	}
	if strings.Contains(page.Body, `value="Music"`) {
		t.Fatal("old skill should be gone")
	}
}

func setupExtensionDB(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "sites.json")
	dbPath := filepath.Join(dir, "example.sqlite")
	raw := `{
  "install_enabled": false,
  "data_root": ` + jsonString(dir) + `,
  "sites": [{
    "id": "example",
    "name": "Example",
    "host": "http://localhost:8001",
    "database": {"type": "sqlite", "dsn": ` + jsonString(dbPath) + `},
    "template_dir": ` + jsonString(filepath.Join(dir, "templates")) + `,
    "assets_dir": ` + jsonString(filepath.Join(dir, "assets")) + `,
    "tmp_dir": ` + jsonString(filepath.Join(dir, "tmp")) + `,
    "language_dir": ` + jsonString(filepath.Join(dir, "language")) + `
  }]
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CANNON_CONFIG", cfgPath)
	oldArgs := os.Args
	os.Args = []string{"volunteer-directory", "--site=example", "--socket=/tmp/volunteer-directory.sock"}
	t.Cleanup(func() { os.Args = oldArgs })
}

func jsonString(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func postWire(t *testing.T, handler http.Handler, path string, wire extension.WireRequest) extension.WireResponse {
	t.Helper()
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d for %s: %s", rec.Code, path, rec.Body.String())
	}
	var resp extension.WireResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode wire response: %v", err)
	}
	return resp
}

func submissionBody(fields map[string]string, skills ...string) string {
	values := url.Values{}
	defaults := map[string]string{
		"name":    "Casey Camp",
		"address": "4 Lake Road",
		"phone":   "555-0199",
		"email":   "casey@example.com",
		"note":    "Happy to help.",
	}
	for key, value := range defaults {
		values.Set(key, value)
	}
	for key, value := range fields {
		if value == "" {
			values.Del(key)
			continue
		}
		values.Set(key, value)
	}
	for _, skill := range skills {
		if skill != "" {
			values.Add("skills", skill)
		}
	}
	return values.Encode()
}

func adminWire(method, rawURL string) extension.WireRequest {
	return adminWireWithBody(method, rawURL, "")
}

func adminWireWithBody(method, rawURL, body string) extension.WireRequest {
	return extension.WireRequest{
		Method: method,
		URL:    rawURL,
		Body:   body,
		CSRF:   "admin-token",
		SiteID: "example",
		User: map[string]any{
			"authenticated": true,
			"email":         "admin@example.com",
		},
	}
}

func adminURL(suffix string) string {
	return "/admin/extension-apps/" + extensionName + suffix
}

func headerValue(resp extension.WireResponse, key string) string {
	for name, values := range resp.Header {
		if strings.EqualFold(name, key) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}
