package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/users"
)

type UserStore interface {
	List(context.Context) ([]users.User, error)
	ByID(context.Context, int64) (users.User, error)
	Save(context.Context, users.User, string) (int64, error)
}

func (a *App) userData(r *http.Request) PageData {
	u, _ := a.Sessions.User(r)
	return PageData{Page: "users", Active: "users", User: u, CSRF: a.Sessions.CSRF(r), Filter: domain.Filter{Branch: u.Branch}}
}
func (a *App) userList(w http.ResponseWriter, r *http.Request) {
	d := a.userData(r)
	var err error
	d.Users, err = a.Users.List(r.Context())
	if err != nil {
		http.Error(w, "Gagal memuat pengguna", 500)
		return
	}
	a.render(w, r, d)
}
func (a *App) userNew(w http.ResponseWriter, r *http.Request) {
	d := a.userData(r)
	d.EditUser = users.User{Role: "USER", IsActive: true}
	d.FormAction = "/users"
	a.render(w, r, d)
}
func (a *App) userEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u, err := a.Users.ByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Gagal memuat pengguna", 500)
		return
	}
	d := a.userData(r)
	d.EditUser = u
	d.FormAction = "/users/" + strconv.FormatInt(id, 10)
	a.render(w, r, d)
}
func (a *App) userCreate(w http.ResponseWriter, r *http.Request) { a.saveUser(w, r, 0) }
func (a *App) userUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	a.saveUser(w, r, id)
}
func (a *App) saveUser(w http.ResponseWriter, r *http.Request, id int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Permintaan tidak valid", 400)
		return
	}
	if !a.Sessions.CheckCSRF(r) {
		http.Error(w, "CSRF tidak valid", 403)
		return
	}
	u := users.User{ID: id, Username: strings.TrimSpace(r.FormValue("username")), FullName: strings.TrimSpace(r.FormValue("full_name")), Position: strings.TrimSpace(r.FormValue("position")), BranchCode: r.FormValue("branch_code"), Role: r.FormValue("role"), IsActive: r.FormValue("is_active") == "on"}
	password := r.FormValue("password")
	if id != 0 {
		if _, err := a.Users.ByID(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, "Gagal memuat pengguna", 500)
			return
		}
	}
	if err := users.Validate(u, password, id == 0); err != nil {
		a.userFormError(w, r, u, err.Error())
		return
	}
	_, err := a.Users.Save(r.Context(), u, password)
	if err != nil {
		if errors.Is(err, users.ErrDuplicate) {
			a.userFormError(w, r, u, "Username sudah digunakan")
			return
		}
		http.Error(w, "Gagal menyimpan pengguna", 500)
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}
func (a *App) userFormError(w http.ResponseWriter, r *http.Request, u users.User, msg string) {
	d := a.userData(r)
	d.EditUser = u
	d.FormAction = "/users"
	d.Error = msg
	if u.ID != 0 {
		d.FormAction = "/users/" + strconv.FormatInt(u.ID, 10)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	a.render(w, r, d)
}
