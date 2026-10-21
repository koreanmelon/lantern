package webui

const stylesheet = `
:root{color-scheme:light;--bg:#f6f8fc;--surface:#fff;--ink:#17243a;--muted:#66758a;--line:#e7ecf3;--blue:#3264e8;--blue-soft:#edf2ff;--green:#259971;--mono:ui-monospace,SFMono-Regular,Consolas,monospace;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:var(--ink);background:var(--bg);font-size:14px}
*{box-sizing:border-box}
body{margin:0}
a{color:var(--blue);text-decoration:none}
a:hover{text-decoration:underline}
button,input,select,textarea{font:inherit}
button,select,summary{cursor:pointer}
a:focus-visible,button:focus-visible,summary:focus-visible{outline:3px solid #9bb6ff;outline-offset:4px}
input:focus,select:focus,textarea:focus{outline:0;border-color:#84a6ff;box-shadow:0 0 0 3px #3264e825}
code{font-family:var(--mono)}
.app{min-height:100vh}
.page-header{background:var(--theme-tint,var(--surface));border-bottom:1px solid var(--line)}
.topbar{display:flex;align-items:center;justify-content:space-between;gap:16px 24px;max-width:1510px;margin:auto;padding:18px 40px;flex-wrap:wrap}
.header-identity{display:flex;align-items:center;gap:24px;flex:1;min-width:0}
.brand{display:flex;align-items:center;gap:10px;font-size:24px;font-weight:650;letter-spacing:-1px;color:var(--ink);flex-shrink:0}
.brand:hover{text-decoration:none}
.brand-mark{display:grid;place-items:center;width:34px;height:38px;border:1px solid var(--theme-brand-border,#f3e2c6);border-radius:10px;background:var(--theme-brand-bg,#fff3df);color:var(--theme-brand-ink,#a57121)}
.brand-mark svg{display:block}
.header-zone{font:12px var(--mono);color:var(--muted);overflow-wrap:anywhere;min-width:0}
.theme-control select{font-size:12px;padding:7px 24px 7px 10px}
.theme-control[hidden]{display:none}
.access-warning{max-width:1510px;margin:0 auto;padding:0 40px 14px;color:var(--muted);font-size:11px;line-height:1.6}
.access-warning strong{font-weight:600}
main{max-width:1510px;margin:0 auto;padding:32px 40px 24px}
.page-heading{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:22px}
h1{font-size:28px;letter-spacing:-.7px;font-weight:650;margin:0;line-height:1.3}
.button{display:inline-flex;align-items:center;justify-content:center;white-space:nowrap;border:1px solid transparent;border-radius:7px;padding:11px 16px;font-size:12px;font-weight:600;line-height:1.4}
.button:hover{text-decoration:none}
.button.primary{background:var(--theme-primary,var(--blue));color:#fff}
.button.primary:hover{background:var(--theme-primary-hover,#2355d7)}
.button.secondary{background:var(--surface);border-color:var(--theme-border,#dfe5ee);color:var(--theme-secondary,#52627a)}
.button.secondary:hover{background:var(--theme-hover,#f0f4fa)}
.button.compact{padding:9px 13px}
.panel{background:var(--surface);border:1px solid var(--line);border-radius:10px;overflow:hidden}
.filters{display:flex;align-items:center;gap:10px;padding:20px}
.search-field{flex:1;max-width:390px;position:relative}
.search-field>span{position:absolute;left:11px;top:6px;font-size:22px;color:var(--muted)}
.search-field input{width:100%;padding-left:35px}
input,textarea,select{min-width:0;background:var(--theme-input,#fff);border:1px solid var(--theme-border,#dfe5ef);border-radius:7px;padding:10px 12px;color:var(--theme-secondary,#344560);font-size:12px;line-height:1.5}
input::placeholder,textarea::placeholder{color:var(--theme-faint,#7c889c)}
select{padding-right:25px}
.reset-link{font-size:12px;padding:8px}
.table-scroll{overflow-x:auto}
table{width:100%;border-collapse:collapse;text-align:left}
thead{background:var(--theme-tint,#f9fbfe);border-top:1px solid var(--line);border-bottom:1px solid var(--line)}
th{font-size:10px;font-weight:600;letter-spacing:.6px;color:var(--muted);padding:12px 18px;white-space:nowrap}
td{padding:15px 18px;border-bottom:1px solid var(--line);font-size:12px;vertical-align:middle}
tbody tr:last-child td{border-bottom:0}
tbody tr:hover{background:var(--theme-hover,#fcfdff)}
.record-name{display:inline-block;font:500 12px var(--mono);min-width:100px;max-width:280px;overflow-wrap:anywhere}
.record-value{display:block;font-size:12px;line-height:1.6;color:var(--theme-secondary,#52627a);overflow-wrap:anywhere;white-space:pre-wrap;max-width:650px;min-width:100px}
.ttl{font:12px var(--mono);white-space:nowrap;color:var(--theme-secondary,#52627a)}
.ttl span{color:var(--muted);margin-left:2px}
.type-badge{display:inline-block;font:600 10px var(--mono);padding:4px 7px;border-radius:5px;background:var(--theme-badge,#eef2f8);color:var(--theme-secondary,#52627a);white-space:nowrap}
.type-A{background:var(--theme-a-bg,#eaf2ff);color:var(--theme-a-ink,#365fa4)}
.type-AAAA{background:var(--theme-aaaa-bg,#e8f6f9);color:var(--theme-aaaa-ink,#2b6b78)}
.type-CNAME{background:var(--theme-alias-bg,#f1edff);color:var(--theme-alias-ink,#71539e)}
.type-MX,.type-SRV{background:var(--theme-mail-bg,#fff4e3);color:var(--theme-mail-ink,#79591f)}
.type-TXT{background:var(--theme-txt-bg,#eaf7ef);color:var(--theme-txt-ink,#38734e)}
.row-actions{display:flex;align-items:center;justify-content:flex-end;gap:16px;white-space:nowrap;font-size:12px}
.row-actions form{margin:0}
.delete-link{border:0;padding:7px 0;background:none;color:var(--muted);font-size:12px}
.delete-link:hover{color:var(--theme-error-ink,#ae3943)}
.table-footer{display:flex;justify-content:space-between;gap:12px;flex-wrap:wrap;padding:16px 20px;border-top:1px solid var(--line);font-size:12px;color:var(--muted)}
.editor{max-width:800px}
.record-form{padding:24px}
.field{display:flex;flex-direction:column;gap:8px;margin-bottom:22px}
.field label{font-size:12px;font-weight:600}
.field label span{font-weight:400;font-size:11px;color:var(--muted);margin-left:5px}
.field input,.field select{padding:11px 12px;font-size:13px}
.field textarea,.field input[type=text]{font-family:var(--mono);font-size:12px}
.field textarea{resize:vertical;min-height:130px;line-height:1.7}
.field small{color:var(--muted);font-size:11px;line-height:1.6}
.form-row{display:grid;grid-template-columns:1fr 1fr;gap:20px}
.form-actions{display:flex;justify-content:flex-end;gap:10px;border-top:1px solid var(--line);padding-top:20px}
.storage-note{font-size:12px;color:var(--muted);margin:16px 0}
.storage-warning{padding:14px 18px;margin:0 0 20px;border:1px solid var(--theme-brand-border,#e6d0a6);border-radius:8px;background:var(--theme-mail-bg,#fff4e3);color:var(--theme-mail-ink,#71501c);font-size:12px;line-height:1.6}
.reference{margin:24px 0 0;border:1px solid var(--line);border-radius:8px;background:var(--surface)}
.reference summary{padding:16px 20px;font-size:12px;font-weight:550}
.reference-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:22px;padding:5px 20px 20px}
.reference-grid p{font-size:12px;line-height:1.6;color:var(--muted)}
.reference-grid code{font-size:11px;line-height:1.7;overflow-wrap:anywhere}
.alert{display:flex;gap:12px;margin-bottom:20px;padding:16px 18px;border:1px solid;border-radius:8px}
.alert strong{font-size:12px;font-weight:600}
.alert p{margin:5px 0 0;font-size:12px;line-height:1.6;overflow-wrap:anywhere}
.alert-icon{display:grid;place-items:center;width:20px;height:20px;border:1px solid;border-radius:50%;font-size:12px;flex-shrink:0}
.alert.error{background:var(--theme-error-bg,#fff3f2);color:var(--theme-error-ink,#ae3943);border-color:var(--theme-error-border,#f5dedc)}
.alert.success{background:var(--theme-success-bg,#eff8f2);color:var(--theme-success-ink,#347550);border-color:var(--theme-success-border,#dceee3)}
.notice-content{display:flex;gap:12px;min-width:0;align-items:flex-start}
.alert.toast{position:fixed;z-index:20;right:max(24px,env(safe-area-inset-right));bottom:max(24px,env(safe-area-inset-bottom));width:400px;max-width:calc(100vw - 48px);max-height:calc(100dvh - 48px);overflow:auto;margin:0;box-shadow:0 10px 35px var(--theme-shadow,#17243a26);align-items:flex-start;gap:12px}
.toast .notice-content{flex:1}
.toast-dismiss{display:grid;place-items:center;flex-shrink:0;width:44px;height:44px;padding:0;border:0;border-radius:7px;background:transparent;color:inherit;font-size:25px;line-height:1}
.toast-dismiss:hover{background:#25997118}
.toast-dismiss[hidden]{display:none}
.empty-state{text-align:center;padding:24px;color:var(--muted)}
.empty-state h2{font-size:15px;font-weight:550;color:var(--ink)}
.empty-state p{font-size:12px}
.sr-only{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}
@media(max-width:900px){main{padding:28px 24px}.topbar{padding:16px 24px}.access-warning{padding:0 24px 14px}.reference-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:650px){main{padding:24px 16px}.topbar{padding:16px;gap:16px}.header-identity{gap:16px}.brand{font-size:22px}.header-zone{font-size:11px}.access-warning{padding:0 16px 12px}.theme-control select{font-size:11px;padding:7px}h1{font-size:24px}.filters{padding:16px;flex-wrap:wrap}.search-field{flex-basis:100%;max-width:none}.filters select{flex:1}.record-form{padding:18px}.form-row{gap:14px}.table-footer{padding:14px 16px}.reference-grid{grid-template-columns:1fr;padding:0 16px 16px;gap:14px}.reference summary{padding:16px}.alert.toast{left:max(16px,env(safe-area-inset-left));right:max(16px,env(safe-area-inset-right));bottom:max(16px,env(safe-area-inset-bottom));width:auto;max-width:none}}
@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;scroll-behavior:auto!important}}
@media(prefers-color-scheme:dark){:root:not([data-theme="light"]){` + darkPalette + `}}
:root[data-theme="dark"]{` + darkPalette + `}
`
