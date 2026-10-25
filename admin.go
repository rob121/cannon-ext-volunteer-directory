package main

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/rob121/cannon/extension"
	"gorm.io/gorm"
)

func (app *directoryApp) admin(req extension.WireRequest) extension.WireResponse {
	if req.User == nil || !extension.UserAuthenticated(req) {
		return extension.Error(http.StatusForbidden, "sign in required")
	}
	base, parts := adminRoute(req.URL)
	if base == "" {
		return extension.Error(http.StatusNotFound, "unknown admin route")
	}
	switch {
	case len(parts) == 0 || (len(parts) == 1 && parts[0] == ""):
		return app.listEntries(req, base)
	case len(parts) == 1 && parts[0] == "skills" && req.Method == http.MethodGet:
		return app.editSkills(req, base)
	case len(parts) == 1 && parts[0] == "skills" && req.Method == http.MethodPost:
		return app.saveSkillEditor(req, base)
	case len(parts) == 1 && parts[0] == "entries":
		return app.listEntries(req, base)
	case len(parts) == 2 && parts[0] == "entries" && req.Method == http.MethodGet:
		return app.showEntry(req, base, parts[1])
	case len(parts) == 3 && parts[0] == "entries" && parts[2] == "delete" && req.Method == http.MethodPost:
		return app.deleteEntry(base, parts[1])
	default:
		return extension.Error(http.StatusNotFound, "unknown admin route")
	}
}

func (app *directoryApp) listEntries(req extension.WireRequest, base string) extension.WireResponse {
	u, err := parseWireURL(req.URL)
	if err != nil {
		return extension.Error(http.StatusBadRequest, err.Error())
	}
	query := strings.TrimSpace(u.Query().Get("q"))
	page := parsePage(u)
	db, err := app.siteDB()
	if err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	filtered := app.searchEntries(db, query)
	var total int64
	if err := filtered.Count(&total).Error; err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	var rows []volunteerEntry
	err = app.searchEntries(db, query).
		Order("created_at desc, entry_id desc").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&rows).Error
	if err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	type rowView struct {
		volunteerEntry
		SkillLabels string
	}
	views := make([]rowView, 0, len(rows))
	for _, row := range rows {
		views = append(views, rowView{volunteerEntry: row, SkillLabels: skillLabels(row)})
	}
	return app.render("volunteer/admin_list.html", map[string]any{
		"Base":    base,
		"Rows":    views,
		"Query":   query,
		"Page":    page,
		"Total":   int(total),
		"HasPrev": page > 1,
		"HasNext": page*pageSize < int(total),
	})
}

func (app *directoryApp) searchEntries(db *gorm.DB, query string) *gorm.DB {
	tx := db.Table(app.entriesTable())
	query = strings.TrimSpace(query)
	if query == "" {
		return tx
	}
	like := "%" + strings.NewReplacer("%", "", "_", "").Replace(query) + "%"
	return tx.Where(
		"name LIKE ? OR email LIKE ? OR phone LIKE ? OR address LIKE ? OR other_skill LIKE ? OR note LIKE ? OR skills_json LIKE ?",
		like, like, like, like, like, like, like,
	)
}

func (app *directoryApp) showEntry(req extension.WireRequest, base, rawID string) extension.WireResponse {
	id, ok := parseID(rawID)
	if !ok {
		return extension.Error(http.StatusNotFound, "entry not found")
	}
	db, err := app.siteDB()
	if err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	var row volunteerEntry
	err = db.Table(app.entriesTable()).Where("entry_id = ?", id).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return extension.Error(http.StatusNotFound, "entry not found")
	}
	if err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	return app.render("volunteer/admin_entry.html", map[string]any{
		"Base":        base,
		"Entry":       row,
		"SkillLabels": skillLabels(row),
		"CSRFField":   csrfField(req),
	})
}

func (app *directoryApp) deleteEntry(base, rawID string) extension.WireResponse {
	id, ok := parseID(rawID)
	if !ok {
		return extension.Error(http.StatusNotFound, "entry not found")
	}
	db, err := app.siteDB()
	if err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	if err := db.Table(app.entriesTable()).Where("entry_id = ?", id).Delete(&volunteerEntry{}).Error; err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	return extension.Redirect(http.StatusSeeOther, base+"?deleted=1")
}

func (app *directoryApp) editSkills(req extension.WireRequest, base string) extension.WireResponse {
	skills, err := app.skills()
	if err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	return app.render("volunteer/admin_skills.html", map[string]any{
		"Base":      base,
		"Skills":    strings.Join(skills, "\n"),
		"CSRFField": csrfField(req),
	})
}

func (app *directoryApp) saveSkillEditor(req extension.WireRequest, base string) extension.WireResponse {
	values, err := url.ParseQuery(req.Body)
	if err != nil {
		return extension.Error(http.StatusBadRequest, "invalid form submission")
	}
	if err := app.saveSkills(skillsFromText(values.Get("skills"))); err != nil {
		return extension.Error(http.StatusInternalServerError, err.Error())
	}
	return extension.Redirect(http.StatusSeeOther, base+"/skills?saved=1")
}

func skillLabels(row volunteerEntry) string {
	return strings.Join(decodeSkills(row.SkillsJSON), ", ")
}
