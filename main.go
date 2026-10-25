package main

import (
	"embed"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rob121/cannon/extension"
	"gorm.io/gorm"
)

//go:embed help/*.md
var helpFiles embed.FS

//go:embed all:templates
var templateFiles embed.FS

const (
	extensionName        = "cannon-ext-volunteer-directory"
	extensionTitle       = "Volunteer Directory"
	extensionDescription = "Collects volunteer and vendor contact details, skills, and notes, then lists them for staff search."
	extensionVersion     = "0.1.0"
	updateURLBase        = "https://github.com/rob121/cannon-ext-volunteer-directory/releases/download"
	defaultAdminMenu     = "Volunteer Directory"
	pageSize             = 20
	skillsSection        = "skills"
	volunteerEvent       = "onVolunteerDirectorySubmit"

	formTitle = "Volunteer and vendor directory"
	formIntro = "Share how you can help the camp, or the services you can offer as a vendor."
)

type directoryApp struct {
	info          extension.Info
	templates     *extension.Templates
	settingsTable string

	dbMu sync.Mutex
	db   *gorm.DB

	migrateMu sync.Mutex
	migrated  bool
}

type volunteerEntry struct {
	ID         uint      `gorm:"primaryKey;column:entry_id"`
	Name       string    `gorm:"column:name;size:200;not null;index"`
	Address    string    `gorm:"column:address;type:text;not null"`
	Phone      string    `gorm:"column:phone;size:64;not null;index"`
	Email      string    `gorm:"column:email;size:320;not null;index"`
	SkillsJSON string    `gorm:"column:skills_json;type:text;not null"`
	OtherSkill string    `gorm:"column:other_skill;size:200"`
	Note       string    `gorm:"column:note;type:text;not null"`
	CreatedAt  time.Time `gorm:"column:created_at"`
}

func main() {
	if err := newServer().Run(); err != nil {
		log.Fatal(err)
	}
}

func newServer() *extension.Server {
	info := extension.Info{
		Name:          extensionName,
		Title:         extensionTitle,
		Description:   extensionDescription,
		Version:       extensionVersion,
		UpdateURLBase: updateURLBase,
		AdminMenuName: defaultAdminMenu,
	}
	app := &directoryApp{
		info: info,
		templates: extension.NewTemplates(templateFiles, "templates").WithFuncs(template.FuncMap{
			"add": func(a, b int) int { return a + b },
			"sub": func(a, b int) int { return a - b },
		}),
		settingsTable: extension.TableName(info.TablePrefix(), "settings"),
	}

	s := extension.New(info)
	s.EmbedHelp(helpFiles, "help")
	s.EmbedTemplates(templateFiles, "templates")
	s.OnInstall(app.install)
	s.RegisterPage(extension.PageDefinition{
		ID:    "volunteer-directory",
		Title: "Volunteer Directory",
	}, app.page)
	s.HandleData("volunteer/submit", app.submit)
	s.HandleAdmin("/admin/", app.admin)
	s.RegisterNotificationEvent(extension.NotificationEventDefinition{
		Event:       volunteerEvent,
		Label:       "Volunteer directory submission",
		Group:       "volunteer-directory",
		GroupLabel:  "Volunteer Directory",
		Description: "Fired after a public volunteer or vendor submission is saved.",
		Placeholders: []extension.NotificationPlaceholder{
			{Key: "name", Label: "Name"},
			{Key: "email", Label: "Email"},
			{Key: "phone", Label: "Phone"},
			{Key: "address", Label: "Address"},
			{Key: "skills", Label: "Skills", Description: "Selected skills, comma-separated"},
			{Key: "other_skill", Label: "Other skill"},
			{Key: "note", Label: "Note"},
			{Key: "admin_url", Label: "Admin URL", Description: "Link to the saved entry"},
			{Key: "site_id", Label: "Site ID"},
		},
	})
	return s
}

func (app *directoryApp) install(req extension.WireRequest) error {
	_, err := app.siteDB()
	return err
}

func (app *directoryApp) siteDB() (*gorm.DB, error) {
	app.dbMu.Lock()
	if app.db != nil {
		db := app.db
		app.dbMu.Unlock()
		return db, nil
	}
	db, _, err := extension.OpenDB()
	if err != nil {
		app.dbMu.Unlock()
		return nil, err
	}
	app.db = db
	app.dbMu.Unlock()
	if err := app.ensureSchema(db); err != nil {
		return nil, err
	}
	return db, nil
}

func (app *directoryApp) ensureSchema(db *gorm.DB) error {
	app.migrateMu.Lock()
	defer app.migrateMu.Unlock()
	if app.migrated {
		return nil
	}
	if err := extension.MigrateConfigurationStore(db, app.settingsTable); err != nil {
		return err
	}
	if err := db.Table(app.entriesTable()).AutoMigrate(&volunteerEntry{}); err != nil {
		return err
	}
	if err := app.ensureDefaultSkills(db); err != nil {
		return err
	}
	app.migrated = true
	return nil
}

func (app *directoryApp) ensureDefaultSkills(db *gorm.DB) error {
	data, err := extension.DBConfigurationStore(db, app.settingsTable).Load(skillsSection)
	if err != nil {
		return err
	}
	if _, ok := data["skills"]; ok {
		return nil
	}
	return extension.DBConfigurationStore(db, app.settingsTable).Save(skillsSection, map[string]any{
		"skills": defaultSkillList(),
	})
}

func defaultSkillList() []string {
	return []string{
		"Kitchen",
		"Maintenance",
		"First aid",
		"Childcare",
		"Music",
		"Photography",
		"Teaching",
		"Setup and cleanup",
	}
}

func (app *directoryApp) skills() ([]string, error) {
	db, err := app.siteDB()
	if err != nil {
		return nil, err
	}
	data, err := extension.DBConfigurationStore(db, app.settingsTable).Load(skillsSection)
	if err != nil {
		return nil, err
	}
	raw, ok := data["skills"]
	if !ok || raw == nil {
		skills := defaultSkillList()
		if err := app.saveSkills(skills); err != nil {
			return nil, err
		}
		return skills, nil
	}
	return parseSkillList(raw), nil
}

func (app *directoryApp) saveSkills(skills []string) error {
	db, err := app.siteDB()
	if err != nil {
		return err
	}
	return extension.DBConfigurationStore(db, app.settingsTable).Save(skillsSection, map[string]any{
		"skills": skills,
	})
}

func parseSkillList(raw any) []string {
	switch values := raw.(type) {
	case []string:
		return uniqueSkills(values)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			text, _ := value.(string)
			out = append(out, text)
		}
		return uniqueSkills(out)
	default:
		return nil
	}
}

func uniqueSkills(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 80 {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func skillsFromText(raw string) []string {
	return uniqueSkills(strings.Split(raw, "\n"))
}

func (app *directoryApp) page(item string, req extension.WireRequest) extension.WireResponse {
	skills, err := app.skills()
	if err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	return app.render("volunteer/form.html", map[string]any{
		"Title":     formTitle,
		"Intro":     formIntro,
		"Skills":    skills,
		"ActionURL": extension.PublicDataURL(extensionName, req.SiteID, "volunteer/submit"),
		"ReturnURL": returnURL(req.URL),
		"Sent":      sentFlag(req.URL),
		"CSRFField": csrfField(req),
		"PageItem":  item,
	})
}

func (app *directoryApp) submit(req extension.WireRequest) extension.WireResponse {
	if !strings.EqualFold(req.Method, http.MethodPost) {
		return extension.Error(http.StatusMethodNotAllowed, "POST required")
	}
	values, err := url.ParseQuery(req.Body)
	if err != nil {
		return extension.Error(http.StatusBadRequest, "invalid form submission")
	}
	skills, err := app.skills()
	if err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	entry, err := entryFromValues(values, skills)
	if err != nil {
		return extension.Error(http.StatusBadRequest, err.Error())
	}
	db, err := app.siteDB()
	if err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	if err := db.Table(app.entriesTable()).Create(&entry).Error; err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}

	redirectURL := strings.TrimSpace(values.Get("return_url"))
	if redirectURL == "" {
		redirectURL = req.URL
	}
	resp := extension.Redirect(http.StatusSeeOther, submitRedirect(redirectURL))
	return extension.WithEmit(resp, volunteerEvent, map[string]any{
		"name":        entry.Name,
		"email":       entry.Email,
		"phone":       entry.Phone,
		"address":     entry.Address,
		"skills":      strings.Join(decodeSkills(entry.SkillsJSON), ", "),
		"other_skill": entry.OtherSkill,
		"note":        entry.Note,
		"admin_url":   "/admin/extension-apps/" + extensionName + "/entries/" + strconv.FormatUint(uint64(entry.ID), 10),
		"site_id":     req.SiteID,
	})
}

func entryFromValues(values url.Values, allowed []string) (volunteerEntry, error) {
	entry := volunteerEntry{
		Name:       strings.TrimSpace(values.Get("name")),
		Address:    strings.TrimSpace(values.Get("address")),
		Phone:      strings.TrimSpace(values.Get("phone")),
		Email:      strings.TrimSpace(values.Get("email")),
		OtherSkill: strings.TrimSpace(values.Get("other_skill")),
		Note:       strings.TrimSpace(values.Get("note")),
	}
	switch {
	case entry.Name == "":
		return entry, errString("Name is required.")
	case entry.Address == "":
		return entry, errString("Address is required.")
	case entry.Phone == "":
		return entry, errString("Phone is required.")
	case entry.Email == "":
		return entry, errString("Email is required.")
	}
	if _, err := mail.ParseAddress(entry.Email); err != nil {
		return entry, errString("Email must be a valid email address.")
	}
	if entry.Note == "" {
		return entry, errString("Note is required.")
	}
	selected, err := selectedSkills(values["skills"], allowed)
	if err != nil {
		return entry, err
	}
	if len(selected) == 0 && entry.OtherSkill == "" {
		return entry, errString("Select at least one skill or describe another skill.")
	}
	raw, err := json.Marshal(selected)
	if err != nil {
		return entry, err
	}
	entry.SkillsJSON = string(raw)
	return entry, nil
}

func selectedSkills(posted []string, allowed []string) ([]string, error) {
	ok := map[string]string{}
	for _, skill := range allowed {
		ok[skill] = skill
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(posted))
	for _, raw := range posted {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		skill, found := ok[raw]
		if !found {
			return nil, errString("One of the selected skills is no longer available.")
		}
		if _, dup := seen[skill]; dup {
			continue
		}
		seen[skill] = struct{}{}
		out = append(out, skill)
	}
	return out, nil
}

type stringError string

func (e stringError) Error() string { return string(e) }

func errString(message string) error { return stringError(message) }

func decodeSkills(raw string) []string {
	var skills []string
	if err := json.Unmarshal([]byte(raw), &skills); err != nil {
		return nil
	}
	return skills
}

func (app *directoryApp) render(name string, data map[string]any) extension.WireResponse {
	body, err := app.templates.Execute(name, data)
	if err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	return extension.HTML(http.StatusOK, body)
}

func (app *directoryApp) entriesTable() string {
	return extension.TableName(app.info.TablePrefix(), "entries")
}

func csrfField(req extension.WireRequest) template.HTML {
	return template.HTML(extension.CSRFHiddenField(req))
}

func submitRedirect(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || strings.TrimSpace(u.Path) == "" {
		return "/?sent=1"
	}
	q := u.Query()
	q.Set("sent", "1")
	u.RawQuery = q.Encode()
	return u.String()
}

func sentFlag(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.TrimSpace(u.Query().Get("sent")) == "1"
}

func returnURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || strings.TrimSpace(u.Path) == "" {
		return "/"
	}
	q := u.Query()
	q.Del("sent")
	u.RawQuery = q.Encode()
	return u.String()
}

func parsePage(u *url.URL) int {
	page, err := strconv.Atoi(u.Query().Get("page"))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

func parseWireURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return url.Parse("/admin/extension-apps/" + url.PathEscape(extensionName))
	}
	return url.Parse(raw)
}

func adminRoute(path string) (string, []string) {
	if u, err := url.Parse(path); err == nil && u.Path != "" {
		path = u.Path
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "admin" && parts[i+1] == "extension-apps" {
			base := "/" + strings.Join(parts[:i+3], "/")
			return base, parts[i+3:]
		}
	}
	return "", nil
}

func parseID(value string) (uint, bool) {
	n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || n == 0 {
		return 0, false
	}
	return uint(n), true
}
