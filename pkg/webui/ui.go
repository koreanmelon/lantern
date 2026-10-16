// Package webui provides Lantern's server-rendered record manager. It has no
// authentication and should be bound to loopback or a trusted network only.
package webui

import (
	"bytes"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/koreanmelon/lantern/pkg/dns/zone"
	"golang.org/x/net/dns/dnsmessage"
)

const DefaultAddress = "127.0.0.1:8080"

type ui struct {
	zone *zone.Zone
	page *template.Template
}

type row struct {
	ID                                        int
	Name, FullName, Type, TTL, Value, EditURL string
}

type pageData struct {
	Origin, Revision, Search, Filter, Error, Notice string
	Total, TypeCount                                int
	Serial                                          uint32
	Records                                         []row
	Types                                           []string
	Form                                            *recordForm
}

// New returns an HTTP handler for the live zone. Cross-origin unsafe requests
// and unrecognized Host headers are rejected, but there is no authentication.
// Loopback hosts are allowed by default. Additional hosts must be trusted names
// or IP literals (without ports), not values derived from incoming requests.
func New(z *zone.Zone, additionalHosts ...string) (http.Handler, error) {
	if z == nil || z.Origin() == "" {
		return nil, fmt.Errorf("web UI requires an initialized zone")
	}
	if err := z.Validate(); err != nil {
		return nil, err
	}
	page, err := template.New("page").Parse(pageTemplate)
	if err != nil {
		return nil, err
	}
	u := &ui{zone: z, page: page}
	hosts := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	for _, host := range additionalHosts {
		host = normalizeHost(host)
		if host == "" {
			continue
		}
		if ip, err := netip.ParseAddr(host); err == nil && ip.IsUnspecified() {
			continue
		}
		hosts[host] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", u.list)
	mux.HandleFunc("GET /records/new", u.newRecord)
	mux.HandleFunc("POST /records", u.save)
	mux.HandleFunc("GET /records/{id}/edit", u.editRecord)
	mux.HandleFunc("POST /records/{id}/edit", u.save)
	mux.HandleFunc("POST /records/{id}/delete", u.deleteRecord)
	mux.HandleFunc("GET /assets/style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		fmt.Fprint(w, stylesheet)
	})
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		if !hosts[normalizeHost(r.Host)] {
			http.Error(w, "Unrecognized dashboard host. Use the configured listen address.", http.StatusMisdirectedRequest)
			return
		}
		protected.ServeHTTP(w, r)
	}), nil
}

func normalizeHost(host string) string {
	if value, _, err := net.SplitHostPort(host); err == nil {
		host = value
	}
	return strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
}

func (u *ui) data(r *http.Request) pageData {
	records, revision := u.zone.Records()
	d := pageData{Origin: u.zone.Origin(), Revision: strconv.FormatUint(revision, 10), Total: len(records), Types: recordTypes,
		Search: r.URL.Query().Get("q"), Filter: r.URL.Query().Get("type")}
	types := make(map[string]bool)
	for id, record := range records {
		typ, value := typeName(record.Header.Type), recordValue(record)
		types[typ] = true
		if body, ok := record.Body.(*dnsmessage.SOAResource); ok {
			d.Serial = body.Serial
		}
		fullName := record.Header.Name.String()
		name := fullName
		canonical, _ := zone.FQDN(fullName)
		if canonical == d.Origin {
			name = "@"
		} else if d.Origin != "." {
			name = strings.TrimSuffix(canonical, "."+d.Origin)
		}
		if d.Filter != "" && d.Filter != typ {
			continue
		}
		if d.Search != "" && !strings.Contains(strings.ToLower(fullName+" "+typ+" "+value), strings.ToLower(d.Search)) {
			continue
		}
		d.Records = append(d.Records, row{ID: id, Name: name, FullName: fullName, Type: typ, TTL: strconv.FormatUint(uint64(record.Header.TTL), 10), Value: value,
			EditURL: fmt.Sprintf("/records/%d/edit?revision=%d", id, revision)})
	}
	d.TypeCount = len(types)
	return d
}

func (u *ui) render(w http.ResponseWriter, status int, d pageData) {
	var output bytes.Buffer
	if err := u.page.Execute(&output, d); err != nil {
		http.Error(w, "Unable to render the record manager", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = output.WriteTo(w)
}

func (u *ui) list(w http.ResponseWriter, r *http.Request) {
	d := u.data(r)
	switch r.URL.Query().Get("notice") {
	case "added":
		d.Notice = "Record added. The DNS server is using it now."
	case "updated":
		d.Notice = "Record updated. The DNS server is using the new value."
	case "deleted":
		d.Notice = "Record deleted from the live zone."
	}
	u.render(w, http.StatusOK, d)
}

func (u *ui) newRecord(w http.ResponseWriter, r *http.Request) {
	d := u.data(r)
	d.Form = &recordForm{Name: "", Type: "A", TTL: "60", Revision: d.Revision, Action: "/records", Title: "Add a record"}
	u.render(w, http.StatusOK, d)
}

func recordIndex(value string, size int) (int, bool) {
	i, err := strconv.Atoi(value)
	return i, err == nil && i >= 0 && i < size
}

func (u *ui) editRecord(w http.ResponseWriter, r *http.Request) {
	records, revision := u.zone.Records()
	if expected := r.URL.Query().Get("revision"); expected != "" && expected != strconv.FormatUint(revision, 10) {
		u.conflict(w, r)
		return
	}
	id, ok := recordIndex(r.PathValue("id"), len(records))
	if !ok {
		http.NotFound(w, r)
		return
	}
	record := records[id]
	if !editable(record) {
		d := u.data(r)
		d.Error = "This record contains data that cannot be represented safely in the text editor. Use the Go zone API to replace it."
		u.render(w, http.StatusUnprocessableEntity, d)
		return
	}
	d := u.data(r)
	d.Form = &recordForm{Name: record.Header.Name.String(), Type: typeName(record.Header.Type), TTL: strconv.FormatUint(uint64(record.Header.TTL), 10), Value: recordValue(record),
		Revision: strconv.FormatUint(revision, 10), Action: fmt.Sprintf("/records/%d/edit", id), Title: "Edit record"}
	u.render(w, http.StatusOK, d)
}

func editable(record dnsmessage.Resource) bool {
	if !safeName(record.Header.Name.String()) {
		return false
	}
	var targets []dnsmessage.Name
	switch body := record.Body.(type) {
	case *dnsmessage.TXTResource:
		if len(body.TXT) == 0 {
			return false
		}
		for _, chunk := range body.TXT {
			if !utf8.ValidString(chunk) {
				return false
			}
		}
		return true
	case *dnsmessage.CNAMEResource:
		targets = []dnsmessage.Name{body.CNAME}
	case *dnsmessage.NSResource:
		targets = []dnsmessage.Name{body.NS}
	case *dnsmessage.PTRResource:
		targets = []dnsmessage.Name{body.PTR}
	case *dnsmessage.MXResource:
		targets = []dnsmessage.Name{body.MX}
	case *dnsmessage.SRVResource:
		targets = []dnsmessage.Name{body.Target}
	case *dnsmessage.SOAResource:
		targets = []dnsmessage.Name{body.NS, body.MBox}
	}
	for _, target := range targets {
		if !safeName(target.String()) {
			return false
		}
	}
	return utf8.ValidString(recordValue(record))
}

func (u *ui) conflict(w http.ResponseWriter, r *http.Request) {
	d := u.data(r)
	d.Error = "The zone changed while you were editing. Nothing was overwritten. Open the record again from this fresh list."
	u.render(w, http.StatusConflict, d)
}

func readForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Unable to read form (maximum size is 64 KiB)", http.StatusBadRequest)
		return false
	}
	return true
}

func (u *ui) save(w http.ResponseWriter, r *http.Request) {
	if !readForm(w, r) {
		return
	}
	records, revision := u.zone.Records()
	expected, err := strconv.ParseUint(r.PostForm.Get("revision"), 10, 64)
	if err != nil {
		http.Error(w, "Missing or invalid zone revision", http.StatusBadRequest)
		return
	}
	if expected != revision {
		u.conflict(w, r)
		return
	}
	form := recordForm{Name: r.PostForm.Get("name"), Type: r.PostForm.Get("type"), TTL: r.PostForm.Get("ttl"), Value: r.PostForm.Get("value"), Revision: r.PostForm.Get("revision"), Action: "/records", Title: "Add a record"}
	id := -1
	if r.PathValue("id") != "" {
		var ok bool
		id, ok = recordIndex(r.PathValue("id"), len(records))
		if !ok {
			http.NotFound(w, r)
			return
		}
		form.Action, form.Title = fmt.Sprintf("/records/%d/edit", id), "Edit record"
	}
	record, err := parseRecord(form, u.zone.Origin())
	if err == nil {
		if id < 0 {
			records = append(records, record)
		} else {
			records[id] = record
		}
		var committed bool
		committed, err = u.zone.ReplaceRecordsIfRevision(expected, records)
		if err == nil && !committed {
			u.conflict(w, r)
			return
		}
	}
	if err != nil {
		d := u.data(r)
		d.Form, d.Error = &form, err.Error()
		u.render(w, http.StatusUnprocessableEntity, d)
		return
	}
	notice := "updated"
	if id < 0 {
		notice = "added"
	}
	http.Redirect(w, r, "/?notice="+notice, http.StatusSeeOther)
}

func (u *ui) deleteRecord(w http.ResponseWriter, r *http.Request) {
	if !readForm(w, r) {
		return
	}
	records, revision := u.zone.Records()
	expected, err := strconv.ParseUint(r.PostForm.Get("revision"), 10, 64)
	if err != nil {
		http.Error(w, "Missing or invalid zone revision", http.StatusBadRequest)
		return
	}
	if expected != revision {
		u.conflict(w, r)
		return
	}
	id, ok := recordIndex(r.PathValue("id"), len(records))
	if !ok {
		http.NotFound(w, r)
		return
	}
	records = append(records[:id], records[id+1:]...)
	committed, err := u.zone.ReplaceRecordsIfRevision(expected, records)
	if err != nil {
		d := u.data(r)
		d.Error = err.Error()
		u.render(w, http.StatusUnprocessableEntity, d)
		return
	}
	if !committed {
		u.conflict(w, r)
		return
	}
	http.Redirect(w, r, "/?notice=deleted", http.StatusSeeOther)
}
