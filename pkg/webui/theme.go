package webui

// Run before styles load so a stored preference applies before the first paint.
// CSS handles system preference changes, including when JavaScript is disabled.
const themeScript = `"use strict";
(() => {
  const key = "lantern-theme";
  const valid = (value) => ["system", "light", "dark"].includes(value) ? value : "system";
  let preference = "system";
  try { preference = valid(window.localStorage.getItem(key)); } catch (_) {}
  document.documentElement.dataset.theme = preference;

  document.addEventListener("DOMContentLoaded", () => {
    const select = document.getElementById("theme-preference");
    if (!select) return;
    const apply = (value) => {
      preference = valid(value);
      document.documentElement.dataset.theme = preference;
      select.value = preference;
    };
    apply(preference);
    select.addEventListener("change", () => {
      apply(select.value);
      try { window.localStorage.setItem(key, preference); } catch (_) {}
    });
    select.closest(".theme-control").hidden = false;
    window.addEventListener("storage", (event) => {
      if (event.key === key || event.key === null) apply(event.newValue);
    });
  });
})();
`

// Keep one dark palette for both the explicit override and system preference.
const darkPalette = `color-scheme:dark;--bg:#0c1422;--surface:#141f31;--ink:#e6edf7;--muted:#a4b3c9;--line:#2c3d55;--blue:#9bbcff;--blue-soft:#223452;--green:#5cd5a4;--theme-secondary:#b7c6dc;--theme-faint:#a1afc3;--theme-input:#0e1929;--theme-border:#3c4f6b;--theme-hover:#1a293f;--theme-tint:#111d2f;--theme-badge:#293a55;--theme-primary:#3667d6;--theme-primary-hover:#3d70df;--theme-brand-bg:#3b301d;--theme-brand-ink:#f0c270;--theme-brand-border:#5a4827;--theme-success-bg:#152f28;--theme-success-ink:#9ae8bd;--theme-success-border:#285b45;--theme-error-bg:#3b222c;--theme-error-ink:#ffb3b5;--theme-error-border:#693847;--theme-a-bg:#213c60;--theme-a-ink:#9dc4ff;--theme-aaaa-bg:#183b46;--theme-aaaa-ink:#8bd9e8;--theme-alias-bg:#332b50;--theme-alias-ink:#ccb9ff;--theme-mail-bg:#41331f;--theme-mail-ink:#f0cb86;--theme-txt-bg:#203e2d;--theme-txt-ink:#a1dfb4;--theme-shadow:#0006;`
