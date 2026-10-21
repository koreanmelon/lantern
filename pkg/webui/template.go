package webui

// Templates, styles, and scripts live in Go so the application ships as one binary with
// no frontend toolchain, asset files, or external network dependencies.
const pageTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light dark">
  <title>{{if .Form}}{{.Form.Title}}{{else}}DNS records{{end}} · Lantern</title>
  <script src="/assets/theme.js"></script>
  <link rel="stylesheet" href="/assets/style.css">
  <script src="/assets/notifications.js" defer></script>
</head>
<body>
<div class="app">
  <div class="workspace">
    <header class="page-header">
      <div class="topbar">
        <div class="header-identity">
          <a class="brand" href="/" aria-label="Lantern home">
            <span class="brand-mark"><svg width="25" height="29" viewBox="0 0 25 29" fill="none" aria-hidden="true"><path d="M8 7V5a4.5 4.5 0 0 1 9 0v2M5 9h15l2 14H3L5 9Z" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><path d="M1 26h23M9 13l-1 7m8-7 1 7" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg></span>
            <span>lantern</span>
          </a>
          <span class="header-zone" aria-label="Current zone">{{.Origin}}</span>
        </div>
        <div class="topbar-meta"><label class="theme-control" hidden><span class="sr-only">Color theme</span><select id="theme-preference"><option value="system">System</option><option value="light">Light</option><option value="dark">Dark</option></select></label></div>
      </div>
      <p class="access-warning"><strong>No authentication.</strong> Use a trusted interface.</p>
    </header>

    <main id="main" tabindex="-1">
      <div class="page-heading">
        <h1 id="page-title">{{if .Form}}{{.Form.Title}}{{else}}DNS records{{end}}</h1>
        {{if not .Form}}<a class="button primary" href="/records/new">Add record</a>{{end}}
      </div>

      {{if .Error}}<div class="alert error" role="alert"><span class="alert-icon">!</span><div><strong>That change wasn’t applied</strong><p>{{.Error}}</p></div></div>{{end}}
      {{if .Notice}}<div class="alert success" data-toast><div class="notice-content" role="status"><span class="alert-icon" aria-hidden="true">✓</span><div><strong>All set</strong><p>{{.Notice}}</p></div></div><button class="toast-dismiss" type="button" aria-label="Dismiss notification" data-dismiss-toast hidden><span aria-hidden="true">×</span></button></div>{{end}}
      {{if not .Persistent}}<p class="storage-warning" role="status" data-storage="memory"><strong>In-memory storage.</strong> Edits will be lost on restart.</p>{{end}}

      {{if .Form}}
        <section class="panel editor" aria-labelledby="page-title">
          <form action="{{.Form.Action}}" method="post" class="record-form">
            <input type="hidden" name="revision" value="{{.Form.Revision}}">
            <div class="form-row">
              <div class="field"><label for="type">Record type</label><select id="type" name="type">{{range .Types}}<option value="{{.}}" {{if eq . $.Form.Type}}selected{{end}}>{{.}}</option>{{end}}</select></div>
              <div class="field"><label for="ttl">TTL <span>seconds</span></label><input id="ttl" name="ttl" type="number" min="0" max="2147483647" value="{{.Form.TTL}}" required><small>How long clients may cache this record.</small></div>
            </div>
            <div class="field"><label for="name">Name</label><input id="name" name="name" type="text" value="{{.Form.Name}}" placeholder="e.g. grafana, @, or grafana.home.arpa." autocomplete="off" spellcheck="false" required autofocus><small>Short names use {{.Origin}}. Use <code>@</code> for the zone apex.</small></div>
            <div class="field"><label for="value">Value <span>record data</span></label><textarea id="value" name="value" rows="5" placeholder="e.g. 192.168.1.20" spellcheck="false">{{.Form.Value}}</textarea><small>Use a trailing dot for absolute target names. See the format guide for structured types.</small></div>
            <div class="form-actions"><a class="button secondary" href="/">Cancel</a><button class="button primary" type="submit">Save record</button></div>
          </form>
        </section>
        {{if .Persistent}}<p class="storage-note" data-storage="file">Changes saved to disk.</p>{{end}}
      {{else}}
      <section class="panel records-panel" aria-labelledby="page-title">
        <form class="filters" action="/" method="get" role="search">
          <div class="search-field"><span aria-hidden="true">⌕</span><label class="sr-only" for="search">Search records</label><input type="search" id="search" name="q" value="{{.Search}}" placeholder="Search names or values…"></div>
          <label class="sr-only" for="filter-type">Filter by record type</label><select id="filter-type" name="type"><option value="">All record types</option>{{range .Types}}<option value="{{.}}" {{if eq . $.Filter}}selected{{end}}>{{.}}</option>{{end}}</select>
          <button class="button secondary compact" type="submit">Filter</button>{{if or .Search .Filter}}<a class="reset-link" href="/">Clear</a>{{end}}
        </form>
        <div class="table-scroll"><table>
          <thead><tr><th scope="col">NAME</th><th scope="col">TYPE</th><th scope="col">VALUE / DESTINATION</th><th scope="col">TTL</th><th scope="col"><span class="sr-only">Actions</span></th></tr></thead>
          <tbody>{{range .Records}}<tr>
            <td class="name-cell"><strong class="record-name" title="{{.FullName}}">{{.Name}}</strong></td>
            <td><span class="type-badge type-{{.Type}}">{{.Type}}</span></td>
            <td><code class="record-value">{{.Value}}</code></td>
            <td class="ttl">{{.TTL}}<span>s</span></td>
            <td><div class="row-actions"><a href="{{.EditURL}}" class="edit-link" aria-label="Edit {{.FullName}} {{.Type}}">Edit</a><form action="/records/{{.ID}}/delete" method="post"><input type="hidden" name="revision" value="{{$.Revision}}"><button type="submit" class="delete-link" aria-label="Delete {{.FullName}} {{.Type}}">Delete</button></form></div></td>
          </tr>{{else}}<tr><td colspan="5"><div class="empty-state"><h2>No matching records</h2><p>Try another search or add a record.</p></div></td></tr>{{end}}</tbody>
        </table></div>
        <div class="table-footer"><span>{{len .Records}} of {{.Total}} records</span>{{if .Persistent}}<span data-storage="file">Changes saved to disk.</span>{{end}}</div>
      </section>
      {{end}}

      <details id="reference" class="reference">
        <summary>Record format guide</summary>
        <div class="reference-grid">
          <div><span class="type-badge type-A">A / AAAA</span><p>An IPv4 or IPv6 address.</p><code>192.168.1.20</code></div>
          <div><span class="type-badge type-CNAME">CNAME / NS / PTR</span><p>A target name. Absolute names end in a dot.</p><code>lantern.home.arpa.</code></div>
          <div><span class="type-badge type-MX">MX</span><p>Preference, then mail-server name.</p><code>10 mail.home.arpa.</code></div>
          <div><span class="type-badge type-SRV">SRV</span><p>Priority, weight, port, target.</p><code>0 5 443 app.home.arpa.</code></div>
          <div><span class="type-badge type-TXT">TXT</span><p>Literal text (up to 255 bytes), or a JSON array for multiple strings.</p><code>["hello", "world"]</code></div>
          <div><span class="type-badge type-SOA">SOA</span><p>Primary server, mailbox name, serial, refresh, retry, expire, minimum TTL.</p><code>ns.home.arpa. hostmaster.home.arpa. 1 3600 600 86400 60</code></div>
        </div>
      </details>
    </main>
  </div>
</div>
</body>
</html>`
