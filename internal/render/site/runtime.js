(function () {
  "use strict";

  var root = document.documentElement;
  var basePath = root.getAttribute("data-obsite-base-path") || "/";
  var storageKey = "obsite.theme.v1:" + basePath;
  var media = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;
  var runtimeScript = document.currentScript;
  var vendorBaseURL = runtimeScript && runtimeScript.src ? new URL("../obsite-runtime/", runtimeScript.src) : null;
  var servedSiteRootURL = runtimeScript && runtimeScript.src ? new URL("../../", runtimeScript.src) : null;

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

  function initSidebar() {
    if (!root.hasAttribute("data-obsite-sidebar") || !runtimeScript || !runtimeScript.src) {
      return;
    }

    var shell = document.querySelector("[data-sidebar-shell]");
    var sidebarRoot = document.querySelector("[data-sidebar-root]");
    var siteBody = document.querySelector("[data-site-body]");
    var toggle = document.querySelector("[data-sidebar-toggle]");
    var closeButton = document.querySelector("[data-sidebar-close]");
    var overlay = document.querySelector("[data-sidebar-overlay]");
    var media = window.matchMedia ? window.matchMedia("(max-width: 56rem)") : null;
    var storageKey = "obsite.sidebar.expanded.v1:" + basePath;
    var expanded = {};
    var currentPath = currentSitePath();
    var popoverEnabled = root.hasAttribute("data-obsite-popover");

    if (!shell || !sidebarRoot || !siteBody) {
      report("error", "Sidebar mount points are unavailable.");
      return;
    }

    window.fetch(new URL("sidebar.json", runtimeScript.src).href, {
      cache: "no-cache",
      headers: {Accept: "application/json"}
    }).then(function (response) {
      if (!response.ok) {
        throw new Error("Sidebar data returned HTTP " + response.status);
      }
      return response.json();
    }).then(function (nodes) {
      if (!Array.isArray(nodes)) {
        throw new Error("Sidebar data is not an array");
      }
      if (!nodes.length) {
        return;
      }

      expanded = readExpandedState();
      sidebarRoot.textContent = "";
      sidebarRoot.appendChild(renderList(nodes, true));
      shell.hidden = false;
      siteBody.setAttribute("data-sidebar-ready", "true");

      if (toggle) {
        toggle.hidden = false;
        toggle.addEventListener("click", function () {
          setMobileSidebarOpen(!isMobileSidebarOpen());
        });
      }
      if (closeButton) {
        closeButton.addEventListener("click", function () {
          setMobileSidebarOpen(false);
        });
      }
      if (overlay) {
        overlay.addEventListener("click", function () {
          setMobileSidebarOpen(false);
        });
      }
      sidebarRoot.addEventListener("click", function (event) {
        var link = event.target.closest("a.sidebar-link");
        if (link && isMobileViewport()) {
          setMobileSidebarOpen(false);
        }
      });
      document.addEventListener("keydown", function (event) {
        if (event.key === "Escape") {
          setMobileSidebarOpen(false);
        }
      });
      if (media && typeof media.addEventListener === "function") {
        media.addEventListener("change", syncViewportState);
      } else if (media && typeof media.addListener === "function") {
        media.addListener(syncViewportState);
      }
      syncViewportState();
    }).catch(function (error) {
      report("error", "Sidebar initialization failed.", error);
    });

    function renderList(items, isRoot) {
      var list = document.createElement("ul");
      list.className = isRoot ? "sidebar-list sidebar-list-root" : "sidebar-list";
      for (var index = 0; index < items.length; index += 1) {
        var node = items[index];
        if (node && typeof node.name === "string" && typeof node.url === "string") {
          list.appendChild(renderNode(node));
        }
      }
      return list;
    }

    function renderNode(node) {
      var item = document.createElement("li");
      var row = document.createElement("div");
      var link = document.createElement("a");
      var children = Array.isArray(node.children) ? node.children : [];
      var isDirectory = node.isDir === true;
      var isExpandable = isDirectory && children.length > 0;
      var key = stableNodeKey(node);
      var isCurrent = key !== "" && key === currentPath;
      var hasStoredExpanded = Object.prototype.hasOwnProperty.call(expanded, key);
      var isExpanded = hasStoredExpanded ? expanded[key] === true : nodeHasCurrent(node);

      item.className = isDirectory ? "sidebar-node sidebar-node-dir" : "sidebar-node sidebar-node-file";
      if (isCurrent) {
        item.classList.add("is-active");
      }
      if (isExpandable) {
        item.setAttribute("data-expanded", String(isExpanded));
      }

      row.className = "sidebar-item";
      row.appendChild(isExpandable ? renderToggle(node, item, isExpanded) : renderToggleSpacer());
      link.className = isDirectory ? "sidebar-link sidebar-link-dir" : "sidebar-link";
      link.href = buildNodeHref(node.url);
      link.textContent = node.name;
      if (popoverEnabled && !isDirectory) {
        link.setAttribute("data-popover-path", key);
      }
      if (isCurrent) {
        link.classList.add("is-current");
        link.setAttribute("aria-current", "page");
      }
      row.appendChild(link);
      item.appendChild(row);

      if (isExpandable) {
        var branch = renderList(children, false);
        branch.hidden = !isExpanded;
        item.appendChild(branch);
      }
      return item;
    }

    function renderToggle(node, item, isExpanded) {
      var button = document.createElement("button");
      var glyph = document.createElement("span");
      var srText = document.createElement("span");
      button.className = "sidebar-toggle";
      button.type = "button";
      button.setAttribute("aria-expanded", String(isExpanded));
      glyph.className = "sidebar-toggle-glyph";
      glyph.setAttribute("aria-hidden", "true");
      glyph.textContent = "▾";
      srText.className = "sr-only";
      srText.textContent = "Toggle " + node.name;
      button.appendChild(glyph);
      button.appendChild(srText);
      button.addEventListener("click", function () {
        var nextExpanded = item.getAttribute("data-expanded") !== "true";
        var branch = item.querySelector(":scope > ul");
        item.setAttribute("data-expanded", String(nextExpanded));
        button.setAttribute("aria-expanded", String(nextExpanded));
        if (branch) {
          branch.hidden = !nextExpanded;
        }
        expanded[stableNodeKey(node)] = nextExpanded;
        persistExpandedState();
      });
      return button;
    }

    function renderToggleSpacer() {
      var spacer = document.createElement("span");
      spacer.className = "sidebar-toggle sidebar-toggle-spacer";
      spacer.setAttribute("aria-hidden", "true");
      return spacer;
    }

    function normalizeSitePath(value) {
      return typeof value === "string" ? value.replace(/^\/+|\/+$/g, "").trim() : "";
    }

    function stableNodeKey(node) {
      return normalizeSitePath(node && node.url);
    }

    function buildNodeHref(rawURL) {
      var cleanURL = normalizeSitePath(rawURL);
      if (!servedSiteRootURL) {
        return cleanURL ? (basePath + cleanURL + "/") : basePath;
      }
      return new URL(cleanURL ? cleanURL + "/" : "./", servedSiteRootURL).href;
    }

    function currentSitePath() {
      var pathname = window.location && window.location.pathname ? window.location.pathname : "/";
      try {
        pathname = decodeURIComponent(pathname);
      } catch (error) {
        report("warn", "Unable to decode the current page URL.", error);
      }
      var cleanBase = servedSiteRootURL && servedSiteRootURL.pathname ? servedSiteRootURL.pathname : basePath;
      try {
        cleanBase = decodeURIComponent(cleanBase);
      } catch (error) {
        report("warn", "Unable to decode the site base path.", error);
      }
      if (pathname.indexOf(cleanBase) === 0) {
        pathname = pathname.slice(cleanBase.length);
      }
      pathname = normalizeSitePath(pathname);
      if (pathname === "index.html") {
        return "";
      }
      pathname = pathname.replace(/\/index\.html$/, "");
      return pathname.replace(/\/page\/[1-9][0-9]*$/, "");
    }

    function nodeHasCurrent(node) {
      if (stableNodeKey(node) === currentPath) {
        return true;
      }
      var children = Array.isArray(node && node.children) ? node.children : [];
      for (var index = 0; index < children.length; index += 1) {
        if (nodeHasCurrent(children[index])) {
          return true;
        }
      }
      return false;
    }

    function readExpandedState() {
      var raw = "";
      try {
        raw = window.localStorage.getItem(storageKey) || "";
      } catch (error) {
        report("warn", "Unable to read Sidebar state.", error);
        return {};
      }
      if (!raw) {
        return {};
      }
      try {
        var parsed = JSON.parse(raw);
        if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
          return parsed;
        }
      } catch (error) {
        report("warn", "Unable to parse Sidebar state.", error);
      }
      return {};
    }

    function persistExpandedState() {
      try {
        window.localStorage.setItem(storageKey, JSON.stringify(expanded));
      } catch (error) {
        report("warn", "Unable to save Sidebar state.", error);
      }
    }

    function isMobileViewport() {
      return !!(media && media.matches);
    }

    function isMobileSidebarOpen() {
      return document.body.getAttribute("data-sidebar-open") === "true";
    }

    function syncSidebarAccessibility(open) {
      var accessible = open || !isMobileViewport();
      if (accessible) {
        shell.removeAttribute("aria-hidden");
        shell.removeAttribute("inert");
        if ("inert" in shell) {
          shell.inert = false;
        }
      } else {
        shell.setAttribute("aria-hidden", "true");
        shell.setAttribute("inert", "");
        if ("inert" in shell) {
          shell.inert = true;
        }
      }
    }

    function setMobileSidebarOpen(open) {
      if (!isMobileViewport()) {
        open = false;
      }
      if (open) {
        document.body.setAttribute("data-sidebar-open", "true");
      } else {
        document.body.removeAttribute("data-sidebar-open");
      }
      syncSidebarAccessibility(open);
      if (overlay) {
        overlay.hidden = !open;
      }
      if (toggle) {
        toggle.setAttribute("aria-expanded", String(open));
      }
    }

    function syncViewportState() {
      setMobileSidebarOpen(false);
    }
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
    initSidebar();
    initMath();
    initMermaid();
  });
})();
