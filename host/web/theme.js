"use strict";

(() => {
  const storageKey = "yourlm-theme";
  const root = document.documentElement;
  const systemTheme = window.matchMedia("(prefers-color-scheme: dark)");

  function storedTheme() {
    try {
      const cookie = document.cookie
        .split(";")
        .map((part) => part.trim())
        .find((part) => part.startsWith(storageKey + "="));
      const value = cookie ? cookie.slice(storageKey.length + 1) : "";
      if (value === "dark" || value === "light") return value;
    } catch {
      // Fall back to per-origin storage when cookies are unavailable.
    }
    try {
      const value = localStorage.getItem(storageKey);
      return value === "dark" || value === "light" ? value : "";
    } catch {
      return "";
    }
  }

  function updateControls(theme) {
    const dark = theme === "dark";
    for (const button of document.querySelectorAll(".themeToggle")) {
      button.setAttribute("aria-pressed", String(dark));
      button.setAttribute("aria-label", dark ? "Włącz tryb jasny" : "Włącz tryb ciemny");
      button.title = dark ? "Włącz tryb jasny" : "Włącz tryb ciemny";
      const label = button.querySelector(".themeLabel");
      if (label) label.textContent = dark ? "Tryb jasny" : "Tryb ciemny";
    }
  }

  function applyTheme(theme, persist) {
    root.dataset.theme = theme;
    const color = document.querySelector('meta[name="theme-color"]');
    if (color) color.content = theme === "dark" ? "#0b110e" : "#f5f7f4";
    if (persist) {
      try {
        document.cookie = `${storageKey}=${theme}; Max-Age=31536000; Path=/; SameSite=Strict`;
      } catch {
        // Per-origin storage below is enough when cookies are blocked.
      }
      try {
        localStorage.setItem(storageKey, theme);
      } catch {
        // The cookie or current document still keeps the selected theme.
      }
    }
    updateControls(theme);
  }

  applyTheme(storedTheme() || (systemTheme.matches ? "dark" : "light"), false);

  document.addEventListener("DOMContentLoaded", () => {
    updateControls(root.dataset.theme);
    for (const button of document.querySelectorAll(".themeToggle")) {
      button.addEventListener("click", () => {
        applyTheme(root.dataset.theme === "dark" ? "light" : "dark", true);
      });
    }
  });

  const followSystem = (event) => {
    if (!storedTheme()) applyTheme(event.matches ? "dark" : "light", false);
  };
  if (systemTheme.addEventListener) systemTheme.addEventListener("change", followSystem);
  else systemTheme.addListener(followSystem);
})();
