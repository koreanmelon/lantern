package webui

// Templates and styles live in Go so the application ships as one binary with
// no frontend toolchain, asset files, or external network dependencies.
const pageTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light">
  <title>{{if .Form}}{{.Form.Title}}{{else}}DNS records{{end}} · Lantern</title>
  <link rel="stylesheet" href="/assets/style.css">
</head>
<body>
<div class="app">
  <aside class="sidebar">
    <a class="brand" href="/" aria-label="Lantern home">
      <span class="brand-mark"><svg width="25" height="29" viewBox="0 0 25 29" fill="none" aria-hidden="true"><path d="M8 7V5a4.5 4.5 0 0 1 9 0v2M5 9h15l2 14H3L5 9Z" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><path d="M1 26h23M9 13l-1 7m8-7 1 7" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg></span>
      <span>lantern<span class="brand-caption">NETWORK WORKSPACE</span></span>
    </a>
    <div class="workspace-label">YOUR WORKSPACE</div>
    <nav aria-label="Main navigation">
      <a href="/" class="nav-item active"><span class="nav-icon">◈</span> DNS records <span class="nav-count">{{.Total}}</span></a>
      <a href="/#reference" class="nav-item"><span class="nav-icon">≡</span> Record reference</a>
    </nav>
    <div class="zone-card">
      <span class="small-label">CURRENT ZONE</span>
      <strong>{{.Origin}}</strong>
      <span><i class="status-dot"></i> Authoritative · IN class</span>
    </div>
    <div class="sidebar-bottom">
      <span class="security-icon">↳</span>
      <div><strong>Your network. Your names.</strong><p>No authentication is enabled. Keep this dashboard on a trusted interface.</p></div>
    </div>
    <div class="sidebar-footer">LANTERN <span>LOCAL DNS</span></div>
  </aside>

  <div class="workspace">
    <header class="topbar">
      <div class="breadcrumb">Workspace <span>/</span> <a href="/">DNS</a>{{if .Form}} <span>/</span> {{.Form.Title}}{{end}}</div>
      <div class="topbar-meta"><span class="live-pill"><i class="status-dot"></i> Live editor</span><span class="revision">Revision {{.Revision}}</span></div>
    </header>

    <main id="main">
      <div class="page-heading">
        <div><div class="eyebrow">A LITTLE ORDER FOR YOUR NETWORK</div><h1>{{if .Form}}{{.Form.Title}}{{else}}DNS records<span class="heading-dot">.</span>{{end}}</h1>
          <p>{{if .Form}}Define where a name leads. Validated changes apply to your running DNS server.{{else}}Give every service a name. Manage the records that connect your homelab.{{end}}</p>
        </div>
        {{if .Form}}<a class="button secondary" href="/">← All records</a>{{else}}<a class="button primary" href="/records/new"><span>＋</span> Add record</a>{{end}}
      </div>

      {{if .Error}}<div class="alert error" role="alert"><span class="alert-icon">!</span><div><strong>That change wasn’t applied</strong><p>{{.Error}}</p></div></div>{{end}}
      {{if .Notice}}<div class="alert success" role="status"><span class="alert-icon">✓</span><div><strong>All set</strong><p>{{.Notice}}</p></div></div>{{end}}

      {{if .Form}}
      <div class="editor-grid">
        <section class="panel editor">
          <div class="panel-heading"><h2>Record details</h2><span class="small-label">{{.Origin}}</span></div>
          <form action="{{.Form.Action}}" method="post" class="record-form">
            <input type="hidden" name="revision" value="{{.Form.Revision}}">
            <div class="form-row">
              <div class="field"><label for="type">Record type</label><select id="type" name="type">{{range .Types}}<option value="{{.}}" {{if eq . $.Form.Type}}selected{{end}}>{{.}}</option>{{end}}</select><small>Choose how the name should resolve.</small></div>
              <div class="field"><label for="ttl">TTL <span>seconds</span></label><input id="ttl" name="ttl" type="number" min="0" max="2147483647" value="{{.Form.TTL}}" required><small>How long clients may cache this record.</small></div>
            </div>
            <div class="field"><label for="name">Name</label><input id="name" name="name" type="text" value="{{.Form.Name}}" placeholder="e.g. grafana, @, or grafana.home.arpa." autocomplete="off" spellcheck="false" required autofocus><small>Short names use {{.Origin}}. Use <code>@</code> for the zone apex.</small></div>
            <div class="field"><label for="value">Value <span>record data</span></label><textarea id="value" name="value" rows="5" placeholder="e.g. 192.168.1.20" spellcheck="false">{{.Form.Value}}</textarea><small>Use a trailing dot for absolute target names. See the format guide for structured types.</small></div>
            <div class="form-actions"><a class="button secondary" href="/">Cancel</a><button class="button primary" type="submit">Save record <span>↗</span></button></div>
          </form>
        </section>
        <aside class="editor-help">
          <div class="help-icon">✦</div><h2>Small change.<br>Instant connection.</h2><p>Every save validates the full zone before it becomes live. Invalid records never replace your working configuration.</p>
          <div class="help-rule"><strong>Keep the essentials</strong><p>Your zone needs exactly one SOA and at least one NS record at its apex.</p></div>
          <div class="help-rule"><strong>One name, one alias</strong><p>A CNAME cannot share its name with other record types.</p></div>
          <div class="help-rule"><strong>Changes are in memory</strong><p>Restarting Lantern restores the startup records. Existing client caches may retain old answers until their TTL expires.</p></div>
        </aside>
      </div>
      {{else}}
      <div class="stats">
        <div class="stat"><div class="stat-label">TOTAL RECORDS <span>↗</span></div><strong>{{.Total}}</strong><span>in your current zone</span></div>
        <div class="stat"><div class="stat-label">RECORD TYPES <span>◈</span></div><strong>{{.TypeCount}}</strong><span>different ways to connect</span></div>
        <div class="stat"><div class="stat-label">SOA SERIAL <span>⟳</span></div><strong>{{.Serial}}</strong><span>zone authority metadata</span></div>
      </div>

      <section class="panel records-panel" aria-labelledby="records-title">
        <div class="panel-heading"><div class="title-with-count"><h2 id="records-title">Zone records</h2><span class="count-pill">{{.Total}}</span></div><span class="zone-label">{{.Origin}}</span></div>
        <form class="filters" action="/" method="get" role="search">
          <div class="search-field"><span aria-hidden="true">⌕</span><label class="sr-only" for="search">Search records</label><input type="search" id="search" name="q" value="{{.Search}}" placeholder="Search names or values…"></div>
          <label class="sr-only" for="filter-type">Filter by record type</label><select id="filter-type" name="type"><option value="">All record types</option>{{range .Types}}<option value="{{.}}" {{if eq . $.Filter}}selected{{end}}>{{.}}</option>{{end}}</select>
          <button class="button secondary compact" type="submit">Filter</button>{{if or .Search .Filter}}<a class="reset-link" href="/">Clear</a>{{end}}
        </form>
        <div class="table-scroll"><table>
          <thead><tr><th scope="col">NAME</th><th scope="col">TYPE</th><th scope="col">VALUE / DESTINATION</th><th scope="col">TTL</th><th scope="col"><span class="sr-only">Actions</span></th></tr></thead>
          <tbody>{{range .Records}}<tr>
            <td class="name-cell"><div class="record-name"><span class="record-symbol">◈</span><span><strong>{{.Name}}</strong><small>{{.FullName}}</small></span></div></td>
            <td><span class="type-badge type-{{.Type}}">{{.Type}}</span></td>
            <td><code class="record-value">{{.Value}}</code></td>
            <td class="ttl">{{.TTL}}<span>s</span></td>
            <td><div class="row-actions"><a href="{{.EditURL}}" class="edit-link" aria-label="Edit {{.FullName}} {{.Type}}">Edit <span>↗</span></a><form action="/records/{{.ID}}/delete" method="post"><input type="hidden" name="revision" value="{{$.Revision}}"><button type="submit" class="delete-link" aria-label="Delete {{.FullName}} {{.Type}}">Delete</button></form></div></td>
          </tr>{{else}}<tr><td colspan="5"><div class="empty-state"><span>⌕</span><h3>No matching records</h3><p>Try another search or create a new record for this zone.</p><a href="/records/new" class="button primary compact">Add record</a></div></td></tr>{{end}}</tbody>
        </table></div>
        <div class="table-footer"><span>{{len .Records}} of {{.Total}} records</span><span><i class="status-dot"></i> Changes apply to the live zone</span></div>
      </section>
      <div class="memory-note"><span>ⓘ</span><p><strong>A workspace, not a config file.</strong> Edits are in memory and reset when Lantern restarts. Client caches follow each record’s TTL.</p><a href="/">Refresh ↻</a></div>
      {{end}}

      <details id="reference" class="reference" {{if .Form}}open{{end}}>
        <summary><span>Record format guide</span><span class="small-label">A LITTLE HELP WITH THE DETAILS ＋</span></summary>
        <div class="reference-grid">
          <div><span class="type-badge type-A">A / AAAA</span><p>An IPv4 or IPv6 address.</p><code>192.168.1.20</code></div>
          <div><span class="type-badge type-CNAME">CNAME / NS / PTR</span><p>A target name. Absolute names end in a dot.</p><code>lantern.home.arpa.</code></div>
          <div><span class="type-badge type-MX">MX</span><p>Preference, then mail-server name.</p><code>10 mail.home.arpa.</code></div>
          <div><span class="type-badge type-SRV">SRV</span><p>Priority, weight, port, target.</p><code>0 5 443 app.home.arpa.</code></div>
          <div><span class="type-badge type-TXT">TXT</span><p>Literal text (up to 255 bytes), or a JSON array for multiple strings.</p><code>["hello", "world"]</code></div>
          <div><span class="type-badge type-SOA">SOA</span><p>Primary server, mailbox name, serial, refresh, retry, expire, minimum TTL.</p><code>ns.home.arpa. hostmaster.home.arpa. 1 3600 600 86400 60</code></div>
        </div>
      </details>
      <footer class="page-footer"><span>Built for the place you call home.</span><span>Lantern · local authoritative DNS</span></footer>
    </main>
  </div>
</div>
</body>
</html>`
