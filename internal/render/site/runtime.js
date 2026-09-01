(function () {
  "use strict";

  var root = document.documentElement;
  var basePath = root.getAttribute("data-obsite-base-path") || "/";
  var storageKey = "obsite.theme.v1:" + basePath;
  var media = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;
  var runtimeScript = document.currentScript;
  var vendorBaseURL = runtimeScript && runtimeScript.src ? new URL("../obsite-runtime/", runtimeScript.src) : null;

  function report(level, message, error) {
    var logger = window.console && window.console[level];
    if (typeof logger === "function") {
      logger.call(window.console, "[obsite] " + message, error || "");
    }
  }

  function isStoredTheme(value) {
    return value === "light" || value === "dark";
  }

  function readStoredTheme() {
    try {
      var value = window.localStorage.getItem(storageKey);
      return isStoredTheme(value) ? value : "";
    } catch (error) {
      report("warn", "Unable to read the saved color mode.", error);
      return "";
    }
  }

  function resolveTheme(preference) {
    if (isStoredTheme(preference)) {
      return preference;
    }
    return media && media.matches ? "dark" : "light";
  }

  function applyTheme(preference) {
    var resolved = resolveTheme(preference);
    if (isStoredTheme(preference)) {
      root.setAttribute("data-theme", preference);
    } else {
      root.removeAttribute("data-theme");
    }
    root.style.colorScheme = resolved;
    return resolved;
  }

  function setToggleText(toggle, selector, value) {
    var element = toggle.querySelector(selector);
    if (element) {
      element.textContent = value;
    }
  }

  function syncThemeToggle(toggle, preference) {
    if (!toggle) {
      return;
    }
    var resolved = resolveTheme(preference);
    setToggleText(toggle, "[data-theme-toggle-value]", resolved === "dark" ? "Dark" : "Light");
    setToggleText(toggle, "[data-theme-toggle-state]", "Current mode " + resolved + ".");
    setToggleText(toggle, "[data-theme-toggle-source]", preference ? "Theme locked to " + preference + "." : "Following system preference.");
    toggle.setAttribute("aria-pressed", String(resolved === "dark"));
    toggle.hidden = false;
  }

  function writeStoredTheme(value) {
    try {
      window.localStorage.setItem(storageKey, value);
    } catch (error) {
      report("warn", "Unable to save the selected color mode.", error);
    }
  }

  function initThemeToggle() {
    var toggle = document.querySelector("[data-theme-toggle]");
    if (!toggle || toggle.getAttribute("data-theme-toggle-ready") === "true") {
      return;
    }
    toggle.setAttribute("data-theme-toggle-ready", "true");
    toggle.addEventListener("click", function () {
      var next = resolveTheme(readStoredTheme()) === "dark" ? "light" : "dark";
      writeStoredTheme(next);
      applyTheme(next);
      syncThemeToggle(toggle, next);
    });
    syncThemeToggle(toggle, readStoredTheme());
  }

  function onReady(callback) {
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", callback, {once: true});
    } else {
      callback();
    }
  }

  function loadScript(url, label) {
    return new Promise(function (resolve, reject) {
      var script = document.createElement("script");
      script.src = url;
      script.defer = true;
      script.addEventListener("load", resolve, {once: true});
      script.addEventListener("error", function () {
        reject(new Error("Failed to load " + label + " from " + url));
      }, {once: true});
      document.head.appendChild(script);
    });
  }

  function initMath() {
    if (!root.hasAttribute("data-obsite-math") || !vendorBaseURL) {
      return;
    }
    loadScript(new URL("katex.min.js", vendorBaseURL).href, "KaTeX")
      .then(function () {
        return loadScript(new URL("auto-render.min.js", vendorBaseURL).href, "KaTeX auto-render");
      })
      .then(function () {
        var target = document.querySelector("[data-page-content]");
        if (!target || typeof window.renderMathInElement !== "function") {
          throw new Error("KaTeX auto-render API is unavailable");
        }
        window.renderMathInElement(target, {
          delimiters: [
            {left: "$$", right: "$$", display: true},
            {left: "\\[", right: "\\]", display: true},
            {left: "$", right: "$", display: false},
            {left: "\\(", right: "\\)", display: false}
          ],
          throwOnError: false
        });
        var failures = target.querySelectorAll(".katex-error");
        for (var index = 0; index < failures.length; index += 1) {
          report("error", "KaTeX could not render formula: " + failures[index].textContent);
        }
      })
      .catch(function (error) {
        report("error", "KaTeX initialization failed.", error);
      });
  }

  function initMermaid() {
    if (!root.hasAttribute("data-obsite-mermaid") || !vendorBaseURL) {
      return;
    }
    loadScript(new URL("mermaid.min.js", vendorBaseURL).href, "Mermaid")
      .then(function () {
        if (!window.mermaid || typeof window.mermaid.run !== "function") {
          throw new Error("Mermaid browser API is unavailable");
        }
        window.mermaid.initialize({startOnLoad: false, theme: "neutral", securityLevel: "loose"});
        var blocks = document.querySelectorAll("pre.mermaid");
        var sequence = Promise.resolve();
        for (var index = 0; index < blocks.length; index += 1) {
          (function (block) {
            sequence = sequence.then(function () {
              var source = block.textContent;
              return Promise.resolve(window.mermaid.run({nodes: [block], suppressErrors: false}))
                .catch(function (error) {
                  if (!block.querySelector("svg")) {
                    block.textContent = source;
                    block.removeAttribute("data-processed");
                  }
                  report("error", "Mermaid could not render diagram.", error);
                });
            });
          })(blocks[index]);
        }
        return sequence;
      })
      .catch(function (error) {
        report("error", "Mermaid initialization failed.", error);
      });
  }

  var initialPreference = readStoredTheme();
  applyTheme(initialPreference);

  if (media) {
    var handleSystemThemeChange = function () {
      if (!readStoredTheme()) {
        applyTheme("");
        syncThemeToggle(document.querySelector("[data-theme-toggle]"), "");
      }
    };
    if (typeof media.addEventListener === "function") {
      media.addEventListener("change", handleSystemThemeChange);
    } else if (typeof media.addListener === "function") {
      media.addListener(handleSystemThemeChange);
    }
  }

  onReady(function () {
    initThemeToggle();
    initMath();
    initMermaid();
  });
})();
