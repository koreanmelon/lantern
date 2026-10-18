package webui

// Success notifications remain inline if JavaScript is unavailable.
const notificationScript = `"use strict";
(() => {
  const toast = document.querySelector("[data-toast]");
  if (!toast) return;
  const dismiss = toast.querySelector("[data-dismiss-toast]");
  if (!dismiss) return;

  dismiss.addEventListener("click", () => {
    const restoreFocus = document.activeElement === dismiss;
    toast.remove();
    if (restoreFocus) document.getElementById("main").focus({ preventScroll: true });

    // Keep search filters and fragments, but do not replay a dismissed notice.
    const url = new URL(window.location.href);
    url.searchParams.delete("notice");
    window.history.replaceState(window.history.state, "", url);
  });
  dismiss.hidden = false;
  toast.classList.add("toast");
})();
`
