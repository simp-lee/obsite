package render

import (
	"encoding/json"
	"html/template"
	"strings"
	"testing"

	"github.com/dop251/goja"
	"github.com/simp-lee/obsite/internal/model"
	xhtml "golang.org/x/net/html"
)

const sidebarRuntimePrelude = `
var __sidebarTest;

function __sidebarTestSetup(fixture) {
  function trimString(value) {
    return String(value == null ? "" : value).replace(/^\s+|\s+$/g, "");
  }

  function trimClassName(value) {
    return trimString(value).replace(/\s+/g, " ");
  }

  function copyMap(obj) {
    var result = {};
    var key = "";

    if (!obj) {
      return result;
    }

    for (key in obj) {
      if (Object.prototype.hasOwnProperty.call(obj, key)) {
        result[key] = String(obj[key]);
      }
    }

    return result;
  }

  function installEventTarget(target, fieldName) {
    target[fieldName] = {};
    target.addEventListener = function (type, handler) {
      var key = String(type || "");

      if (!key || typeof handler !== "function") {
        return;
      }

      if (!this[fieldName][key]) {
        this[fieldName][key] = [];
      }
      this[fieldName][key].push(handler);
    };

    target.dispatchEvent = function (event) {
      var key = "";
      var handlers = [];
      var index = 0;

      if (!event || !event.type) {
        return true;
      }

      key = String(event.type);
      handlers = this[fieldName][key] || [];
      if (!event.target) {
        event.target = this;
      }
      event.currentTarget = this;

      for (index = 0; index < handlers.length; index++) {
        handlers[index].call(this, event);
      }

      return true;
    };
  }

  function ClassList(node) {
    this.node = node;
  }

  ClassList.prototype.add = function (name) {
    var next = trimString(name);
    var className = trimClassName(this.node.className);
    var parts = className ? className.split(" ") : [];
    var index = 0;

    if (!next) {
      return;
    }

    for (index = 0; index < parts.length; index++) {
      if (parts[index] === next) {
        return;
      }
    }

    parts.push(next);
    this.node.className = trimClassName(parts.join(" "));
  };

  function Element(tagName) {
    this.tagName = trimString(tagName).toLowerCase();
    this.children = [];
    this.parentNode = null;
    this.attributes = {};
    this.className = "";
    this.hidden = false;
    this.listeners = {};
    this._textContent = "";
    this.href = "";
    this.type = "";
    this.id = "";
    this.classList = new ClassList(this);
  }

  Object.defineProperty(Element.prototype, "textContent", {
    get: function () {
      var result = this._textContent || "";
      var index = 0;

      for (index = 0; index < this.children.length; index++) {
        result += this.children[index].textContent || "";
      }

      return result;
    },
    set: function (value) {
      this._textContent = String(value == null ? "" : value);
      this.children = [];
    }
  });

  Element.prototype.appendChild = function (child) {
    if (!child) {
      return child;
    }

    child.parentNode = this;
    this.children.push(child);
    return child;
  };

  Element.prototype.setAttribute = function (name, value) {
    var key = String(name || "");
    var next = String(value == null ? "" : value);

    if (!key) {
      return;
    }

    this.attributes[key] = next;
    if (key === "class") {
      this.className = next;
    } else if (key === "id") {
      this.id = next;
    } else if (key === "href") {
      this.href = next;
    } else if (key === "type") {
      this.type = next;
    }
  };

  Element.prototype.getAttribute = function (name) {
    var key = String(name || "");

    if (!key) {
      return null;
    }
    if (Object.prototype.hasOwnProperty.call(this.attributes, key)) {
      return this.attributes[key];
    }
    if (key === "class" && this.className) {
      return this.className;
    }
    if (key === "id" && this.id) {
      return this.id;
    }
    if (key === "href" && this.href) {
      return this.href;
    }
    if (key === "type" && this.type) {
      return this.type;
    }

    return null;
  };

  Element.prototype.removeAttribute = function (name) {
    var key = String(name || "");

    if (!key) {
      return;
    }

    delete this.attributes[key];
    if (key === "class") {
      this.className = "";
    } else if (key === "id") {
      this.id = "";
    } else if (key === "href") {
      this.href = "";
    } else if (key === "type") {
      this.type = "";
    }
  };

  Element.prototype.addEventListener = function (type, handler) {
    var key = String(type || "");

    if (!key || typeof handler !== "function") {
      return;
    }

    if (!this.listeners[key]) {
      this.listeners[key] = [];
    }
    this.listeners[key].push(handler);
  };

  Element.prototype.dispatchEvent = function (event) {
    var key = "";
    var handlers = [];
    var index = 0;

    if (!event || !event.type) {
      return true;
    }

    key = String(event.type);
    handlers = this.listeners[key] || [];
    if (!event.target) {
      event.target = this;
    }
    event.currentTarget = this;

    for (index = 0; index < handlers.length; index++) {
      handlers[index].call(this, event);
    }

    return true;
  };

  function hasClass(node, className) {
    var classes = trimClassName(node && node.className);

    if (!classes || !className) {
      return false;
    }

    return (" " + classes + " ").indexOf(" " + className + " ") >= 0;
  }

  function matchesSelector(node, selector) {
    var match = null;
    var tagName = "";
    var className = "";
    var attrValue = null;

    selector = trimString(selector);
    if (!selector || !node || !node.tagName) {
      return false;
    }

    match = selector.match(/^\[([^\]=]+)(?:="([^"]*)")?\]$/);
    if (match) {
      attrValue = node.getAttribute(match[1]);
      if (attrValue == null) {
        return false;
      }
      if (typeof match[2] === "string") {
        return attrValue === match[2];
      }
      return true;
    }

    match = selector.match(/^([a-z0-9_-]+)\.([a-z0-9_-]+)$/i);
    if (match) {
      tagName = match[1].toLowerCase();
      className = match[2];
      return node.tagName === tagName && hasClass(node, className);
    }

    if (selector.charAt(0) === ".") {
      return hasClass(node, selector.slice(1));
    }

    return node.tagName === selector.toLowerCase();
  }

  function collectDescendants(node, selector, results) {
    var index = 0;
    var child = null;

    if (!node || !node.children) {
      return;
    }

    for (index = 0; index < node.children.length; index++) {
      child = node.children[index];
      if (matchesSelector(child, selector)) {
        results.push(child);
      }
      collectDescendants(child, selector, results);
    }
  }

  function queryWithin(node, selector) {
    var results = [];
    var directSelector = "";
    var index = 0;
    var child = null;

    if (!node) {
      return results;
    }

    selector = trimString(selector);
    if (!selector) {
      return results;
    }

    if (selector.indexOf(":scope > ") === 0) {
      directSelector = trimString(selector.slice(9));
      for (index = 0; index < node.children.length; index++) {
        child = node.children[index];
        if (matchesSelector(child, directSelector)) {
          results.push(child);
        }
      }
      return results;
    }

    collectDescendants(node, selector, results);
    return results;
  }

  Element.prototype.querySelector = function (selector) {
    var results = queryWithin(this, selector);
    return results.length ? results[0] : null;
  };

  Element.prototype.querySelectorAll = function (selector) {
    return queryWithin(this, selector);
  };

  Element.prototype.closest = function (selector) {
    var current = this;

    while (current) {
      if (matchesSelector(current, selector)) {
        return current;
      }
      current = current.parentNode;
    }

    return null;
  };

  function findByID(node, id) {
    var index = 0;
    var found = null;

    if (!node) {
      return null;
    }
    if (node.id === id) {
      return node;
    }

    for (index = 0; index < node.children.length; index++) {
      found = findByID(node.children[index], id);
      if (found) {
        return found;
      }
    }

    return null;
  }

  function createDocument(fixtureData) {
    var doc = {};

    installEventTarget(doc, "_listeners");
    doc.baseURI = fixtureData.baseURI || "https://example.com/blog/current/index.html";
    doc.body = new Element("body");
    doc.createElement = function (tagName) {
      return new Element(tagName);
    };
    doc.getElementById = function (id) {
      return findByID(doc.body, String(id || ""));
    };
    doc.querySelector = function (selector) {
      var results = queryWithin(doc.body, selector);
      return results.length ? results[0] : null;
    };
    doc.querySelectorAll = function (selector) {
      return queryWithin(doc.body, selector);
    };

    return doc;
  }

  function createWindow(doc, fixtureData) {
    var win = {};

    installEventTarget(win, "_listeners");
    win.document = doc;
    win.window = win;
    win.clearTimeout = function () {};
    win.setTimeout = function (handler) {
      if (typeof handler === "function") {
        handler();
      }
      return 1;
    };
    win.matchMedia = function () {
      return {
        matches: !!fixtureData.mobile,
        addEventListener: function () {},
        addListener: function () {}
      };
    };

    return win;
  }

  function createStorage(initial) {
    return {
      _store: copyMap(initial),
      getItem: function (key) {
        var cleanKey = String(key || "");

        if (Object.prototype.hasOwnProperty.call(this._store, cleanKey)) {
          return this._store[cleanKey];
        }
        return null;
      },
      setItem: function (key, value) {
        this._store[String(key || "")] = String(value == null ? "" : value);
      },
      removeItem: function (key) {
        delete this._store[String(key || "")];
      }
    };
  }

  function appendElement(parent, tagName, attrs, textContent, hidden) {
    var element = new Element(tagName);
    var key = "";

    if (attrs) {
      for (key in attrs) {
        if (Object.prototype.hasOwnProperty.call(attrs, key)) {
          element.setAttribute(key, attrs[key]);
        }
      }
    }
    if (typeof textContent === "string") {
      element.textContent = textContent;
    }
    if (hidden) {
      element.hidden = true;
    }
    if (parent) {
      parent.appendChild(element);
    }

    return element;
  }

  function buildFixtureDOM(doc, fixtureData) {
    var body = doc.body;
    var siteBody = appendElement(body, "div", {"data-site-body": "", "class": "site-body"}, "", false);
    var shell = appendElement(siteBody, "aside", {"data-sidebar-shell": "", "id": "sidebar-panel", "class": "sidebar-shell"}, "", true);

    appendElement(shell, "button", {"data-sidebar-close": "", "class": "sidebar-close"}, "Close", false);
    appendElement(shell, "nav", {"data-sidebar-root": "", "data-site-root-rel": fixtureData.siteRootRel || "./", "class": "sidebar"}, "", false);
    appendElement(body, "button", {"data-sidebar-toggle": "", "class": "sidebar-launch"}, "Browse", true);
    appendElement(body, "div", {"data-sidebar-overlay": "", "class": "sidebar-overlay"}, "", true);
    appendElement(body, "script", {"id": "sidebar-data", "type": "application/json"}, fixtureData.sidebarJSON || "[]", false);

    if (fixtureData.popoverEnabled) {
      appendElement(body, "aside", {
        "data-popover-card": "",
        "data-site-root-rel": fixtureData.siteRootRel || "./",
        "data-popover-root": fixtureData.popoverRoot || ((fixtureData.siteRootRel || "./") + "_popover/")
      }, "", true);
    }
  }

  function findFirstByTag(node, tagName) {
    var index = 0;
    var found = null;

    if (!node) {
      return null;
    }
    if (node.tagName === tagName) {
      return node;
    }

    for (index = 0; index < node.children.length; index++) {
      found = findFirstByTag(node.children[index], tagName);
      if (found) {
        return found;
      }
    }

    return null;
  }

  function findLinkByText(node, label) {
    var index = 0;
    var found = null;

    if (!node) {
      return null;
    }
    if (node.tagName === "a" && node.textContent === label) {
      return node;
    }

    for (index = 0; index < node.children.length; index++) {
      found = findLinkByText(node.children[index], label);
      if (found) {
        return found;
      }
    }

    return null;
  }

  function findItemByLabel(node, label) {
    var row = null;
    var link = null;
    var index = 0;
    var found = null;

    if (!node) {
      return null;
    }
    if (node.tagName === "li" && hasClass(node, "sidebar-node")) {
      row = findFirstByTag(node, "div");
      link = findLinkByText(row, label);
      if (link) {
        return node;
      }
    }

    for (index = 0; index < node.children.length; index++) {
      found = findItemByLabel(node.children[index], label);
      if (found) {
        return found;
      }
    }

    return null;
  }

  function listenerTypes(node) {
    var result = [];
    var key = "";

    if (!node || !node.listeners) {
      return result;
    }

    for (key in node.listeners) {
      if (Object.prototype.hasOwnProperty.call(node.listeners, key) && node.listeners[key] && node.listeners[key].length) {
        result.push(key);
      }
    }
    result.sort();

    return result;
  }

  function summarizeItem(item) {
    var row = findFirstByTag(item, "div");
    var branch = findFirstByTag(item, "ul");
    var link = row ? findFirstByTag(row, "a") : null;

    return {
      label: link ? link.textContent : "",
      href: link ? (link.href || "") : "",
      className: link ? (link.className || "") : "",
      isCurrent: !!(link && link.getAttribute("aria-current") === "page"),
      isDir: hasClass(item, "sidebar-node-dir"),
      expanded: item.getAttribute("data-expanded") || "",
      hasBranch: !!branch,
      branchHidden: !!(branch && branch.hidden),
      children: branch ? summarizeList(branch) : []
    };
  }

  function summarizeList(list) {
    var result = [];
    var index = 0;

    if (!list) {
      return result;
    }

    for (index = 0; index < list.children.length; index++) {
      if (list.children[index].tagName === "li") {
        result.push(summarizeItem(list.children[index]));
      }
    }

    return result;
  }

  function sidebarStateSnapshot() {
    var shell = document.querySelector("[data-sidebar-shell]");
    var overlay = document.querySelector("[data-sidebar-overlay]");
    var toggle = document.querySelector("[data-sidebar-toggle]");

    return {
      shellHidden: !!(shell && shell.hidden),
      shellAttrs: shell ? copyMap(shell.attributes) : {},
      overlayHidden: !!(overlay && overlay.hidden),
      toggleHidden: !!(toggle && toggle.hidden),
      toggleExpanded: toggle ? (toggle.getAttribute("aria-expanded") || "") : "",
      bodyAttrs: document.body ? copyMap(document.body.attributes) : {}
    };
  }

  document = createDocument(fixture);
  window = createWindow(document, fixture);
  localStorage = createStorage(fixture.localStorage || {});
  window.localStorage = localStorage;
  buildFixtureDOM(document, fixture);

  __sidebarTest = {
    sidebarReady: function () {
      var node = document.querySelector("[data-site-body]");
      return node ? (node.getAttribute("data-sidebar-ready") || "") : "";
    },
    sidebarState: function () {
      return sidebarStateSnapshot();
    },
    sidebarTree: function () {
      var root = document.querySelector("[data-sidebar-root]");
      var list = root ? findFirstByTag(root, "ul") : null;
      return summarizeList(list);
    },
    clickSidebarToggle: function () {
      var button = document.querySelector("[data-sidebar-toggle]");

      if (!button) {
        return false;
      }

      button.dispatchEvent({type: "click", target: button});
      return true;
    },
    clickSidebarClose: function () {
      var button = document.querySelector("[data-sidebar-close]");

      if (!button) {
        return false;
      }

      button.dispatchEvent({type: "click", target: button});
      return true;
    },
    clickToggleFor: function (label) {
      var root = document.querySelector("[data-sidebar-root]");
      var item = findItemByLabel(root, label);
      var row = item ? findFirstByTag(item, "div") : null;
      var button = row ? findFirstByTag(row, "button") : null;

      if (!button) {
        return false;
      }

      button.dispatchEvent({type: "click", target: button});
      return true;
    },
    storage: function () {
      return copyMap(localStorage._store);
    },
    linkSnapshot: function (label) {
      var link = findLinkByText(document.body, label);

      if (!link) {
        return null;
      }

      return {
        label: link.textContent,
        href: link.href || "",
        attrs: copyMap(link.attributes),
        listenerTypes: listenerTypes(link)
      };
    }
  };
  window.__sidebarTest = __sidebarTest;
}
`

type sidebarRuntimeFixture struct {
	SidebarJSON    string            `json:"sidebarJSON"`
	SiteRootRel    string            `json:"siteRootRel"`
	PopoverEnabled bool              `json:"popoverEnabled"`
	PopoverRoot    string            `json:"popoverRoot"`
	BaseURI        string            `json:"baseURI"`
	LocalStorage   map[string]string `json:"localStorage"`
	Mobile         bool              `json:"mobile"`
}

type sidebarRenderedItem struct {
	Label        string                `json:"label"`
	Href         string                `json:"href"`
	ClassName    string                `json:"className"`
	IsCurrent    bool                  `json:"isCurrent"`
	IsDir        bool                  `json:"isDir"`
	Expanded     string                `json:"expanded"`
	HasBranch    bool                  `json:"hasBranch"`
	BranchHidden bool                  `json:"branchHidden"`
	Children     []sidebarRenderedItem `json:"children"`
}

type sidebarLinkSnapshot struct {
	Label         string            `json:"label"`
	Href          string            `json:"href"`
	Attrs         map[string]string `json:"attrs"`
	ListenerTypes []string          `json:"listenerTypes"`
}

type sidebarStateSnapshot struct {
	ShellHidden    bool              `json:"shellHidden"`
	ShellAttrs     map[string]string `json:"shellAttrs"`
	OverlayHidden  bool              `json:"overlayHidden"`
	ToggleHidden   bool              `json:"toggleHidden"`
	ToggleExpanded string            `json:"toggleExpanded"`
	BodyAttrs      map[string]string `json:"bodyAttrs"`
}

type sidebarRuntimeHarness struct {
	t  *testing.T
	vm *goja.Runtime
}

func TestSidebarRuntimeRendersNestedTreeTracksExpansionAndCurrentPage(t *testing.T) {
	t.Parallel()

	const scopedStorageKey = "obsite.sidebar.expanded.v1:/blog/"

	tmpl := parseDefaultTemplateSet(t)
	html := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			BaseURL:  "https://example.com/blog/",
			Language: "en",
			Sidebar:  model.SidebarConfig{Enabled: true},
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
		SidebarTree: []model.SidebarNode{
			{
				Name:  "docs",
				URL:   "docs/",
				IsDir: true,
				Children: []model.SidebarNode{{
					Name: "Reference",
					URL:  "reference/",
				}},
			},
			{
				Name:  "notes",
				URL:   "notes/",
				IsDir: true,
				Children: []model.SidebarNode{
					{
						Name:  "alpha",
						URL:   "notes/alpha/",
						IsDir: true,
						Children: []model.SidebarNode{{
							Name:     "Guide",
							URL:      "guide/",
							IsActive: true,
						}},
					},
					{
						Name: "Beta",
						URL:  "beta/",
					},
				},
			},
			{
				Name: "Root",
				URL:  "root/",
			},
		},
	})

	harness := newSidebarRuntimeHarness(t, html, nil, false, false)
	if got := harness.sidebarReady(); got != "true" {
		t.Fatalf("sidebar ready marker = %q, want %q", got, "true")
	}

	tree := harness.sidebarTree()
	if len(tree) != 3 {
		t.Fatalf("len(sidebar tree) = %d, want %d: %#v", len(tree), 3, tree)
	}

	docs := requireSidebarItem(t, tree, "docs")
	if !docs.IsDir || docs.Expanded != "false" || !docs.HasBranch || !docs.BranchHidden {
		t.Fatalf("docs sidebar item = %#v, want collapsed directory branch", docs)
	}

	notes := requireSidebarItem(t, tree, "notes")
	if !notes.IsDir || notes.Expanded != "true" || notes.BranchHidden {
		t.Fatalf("notes sidebar item = %#v, want expanded active ancestor", notes)
	}

	alpha := requireSidebarItem(t, tree, "alpha")
	if !alpha.IsDir || alpha.Expanded != "true" || alpha.BranchHidden {
		t.Fatalf("alpha sidebar item = %#v, want expanded nested active ancestor", alpha)
	}

	guide := requireSidebarItem(t, tree, "Guide")
	if guide.IsDir || !guide.IsCurrent || guide.Href != "../guide/" {
		t.Fatalf("guide sidebar item = %#v, want current note link with rooted href", guide)
	}

	if !harness.clickToggleFor("docs") {
		t.Fatal("clickToggleFor(docs) = false, want true")
	}

	expandedStorage := harness.storage()
	if got := expandedStorage[scopedStorageKey]; !strings.Contains(got, `"docs":true`) {
		t.Fatalf("expanded sidebar storage = %q, want docs persisted as true", got)
	}
	if _, ok := expandedStorage["obsite.sidebar.expanded.v1"]; ok {
		t.Fatalf("expanded sidebar storage unexpectedly retained legacy key: %#v", expandedStorage)
	}

	tree = harness.sidebarTree()
	docs = requireSidebarItem(t, tree, "docs")
	if docs.Expanded != "true" || docs.BranchHidden {
		t.Fatalf("docs sidebar item after expand = %#v, want open branch", docs)
	}

	harness = newSidebarRuntimeHarness(t, html, expandedStorage, false, false)
	tree = harness.sidebarTree()
	docs = requireSidebarItem(t, tree, "docs")
	if docs.Expanded != "true" || docs.BranchHidden {
		t.Fatalf("docs sidebar item after reload = %#v, want persisted open branch", docs)
	}

	if !harness.clickToggleFor("docs") {
		t.Fatal("clickToggleFor(docs) on reload = false, want true")
	}

	collapsedStorage := harness.storage()
	if got := collapsedStorage[scopedStorageKey]; !strings.Contains(got, `"docs":false`) {
		t.Fatalf("collapsed sidebar storage = %q, want docs persisted as false", got)
	}
	if _, ok := collapsedStorage["obsite.sidebar.expanded.v1"]; ok {
		t.Fatalf("collapsed sidebar storage unexpectedly retained legacy key: %#v", collapsedStorage)
	}

	harness = newSidebarRuntimeHarness(t, html, collapsedStorage, false, false)
	tree = harness.sidebarTree()
	docs = requireSidebarItem(t, tree, "docs")
	if docs.Expanded != "false" || !docs.BranchHidden {
		t.Fatalf("docs sidebar item after collapse reload = %#v, want persisted collapsed branch", docs)
	}

	notes = requireSidebarItem(t, tree, "notes")
	if notes.Expanded != "true" || notes.BranchHidden {
		t.Fatalf("notes sidebar item after collapse reload = %#v, want active branch still expanded", notes)
	}

	guide = requireSidebarItem(t, tree, "Guide")
	if !guide.IsCurrent {
		t.Fatalf("guide sidebar item after collapse reload = %#v, want current note highlight preserved", guide)
	}
}

func TestSidebarRuntimeStoredCollapseOverridesActiveBranchAutoOpen(t *testing.T) {
	t.Parallel()

	const scopedStorageKey = "obsite.sidebar.expanded.v1:/blog/"

	tmpl := parseDefaultTemplateSet(t)
	html := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			BaseURL:  "https://example.com/blog/",
			Language: "en",
			Sidebar:  model.SidebarConfig{Enabled: true},
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
		SidebarTree: []model.SidebarNode{{
			Name:  "notes",
			URL:   "notes/",
			IsDir: true,
			Children: []model.SidebarNode{{
				Name:  "alpha",
				URL:   "notes/alpha/",
				IsDir: true,
				Children: []model.SidebarNode{{
					Name:     "Guide",
					URL:      "guide/",
					IsActive: true,
				}},
			}},
		}},
	})

	harness := newSidebarRuntimeHarness(t, html, map[string]string{
		"obsite.sidebar.expanded.v1": `{"notes":false,"notes/alpha":false}`,
	}, false, false)
	storage := harness.storage()
	if got := storage[scopedStorageKey]; !strings.Contains(got, `"notes":false`) || !strings.Contains(got, `"notes/alpha":false`) {
		t.Fatalf("migrated sidebar storage = %q, want scoped collapsed state", got)
	}
	if _, ok := storage["obsite.sidebar.expanded.v1"]; ok {
		t.Fatalf("migrated sidebar storage unexpectedly retained legacy key: %#v", storage)
	}

	tree := harness.sidebarTree()
	notes := requireSidebarItem(t, tree, "notes")
	if !notes.IsDir || notes.Expanded != "false" || !notes.BranchHidden {
		t.Fatalf("notes sidebar item = %#v, want stored collapsed active ancestor", notes)
	}

	alpha := requireSidebarItem(t, tree, "alpha")
	if !alpha.IsDir || alpha.Expanded != "false" || !alpha.BranchHidden {
		t.Fatalf("alpha sidebar item = %#v, want stored collapsed nested active ancestor", alpha)
	}

	guide := requireSidebarItem(t, tree, "Guide")
	if !guide.IsCurrent {
		t.Fatalf("guide sidebar item = %#v, want current note highlight preserved under stored collapse", guide)
	}
}

func TestSidebarRuntimeMobileClosedStateTogglesAccessibilityState(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	html := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			Language: "en",
			Sidebar:  model.SidebarConfig{Enabled: true},
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
		SidebarTree: []model.SidebarNode{{
			Name:  "notes",
			URL:   "notes/",
			IsDir: true,
			Children: []model.SidebarNode{{
				Name:     "Guide",
				URL:      "guide/",
				IsActive: true,
			}},
		}},
	})

	requireClosedState := func(state sidebarStateSnapshot, label string) {
		t.Helper()

		_, hasInert := state.ShellAttrs["inert"]
		if !state.ShellHidden && !hasInert {
			t.Fatalf("%s sidebar state = %#v, want mobile closed state removed from keyboard reachability", label, state)
		}
		if got := state.ShellAttrs["aria-hidden"]; got != "true" {
			t.Fatalf("%s aria-hidden = %q, want %q", label, got, "true")
		}
		if state.ToggleExpanded != "false" {
			t.Fatalf("%s toggle aria-expanded = %q, want %q", label, state.ToggleExpanded, "false")
		}
		if !state.OverlayHidden {
			t.Fatalf("%s overlayHidden = %t, want true", label, state.OverlayHidden)
		}
	}

	mobile := newSidebarRuntimeHarness(t, html, nil, false, true)
	state := mobile.sidebarState()
	if state.ToggleHidden {
		t.Fatalf("mobile toggleHidden = %t, want false after sidebar init", state.ToggleHidden)
	}
	requireClosedState(state, "mobile default closed")

	if !mobile.clickSidebarToggle() {
		t.Fatal("clickSidebarToggle() = false, want true")
	}

	state = mobile.sidebarState()
	if state.ShellHidden {
		t.Fatalf("mobile open shellHidden = %t, want false", state.ShellHidden)
	}
	if _, ok := state.ShellAttrs["aria-hidden"]; ok {
		t.Fatalf("mobile open shell attrs = %#v, want aria-hidden removed", state.ShellAttrs)
	}
	if _, ok := state.ShellAttrs["inert"]; ok {
		t.Fatalf("mobile open shell attrs = %#v, want inert removed", state.ShellAttrs)
	}
	if state.ToggleExpanded != "true" {
		t.Fatalf("mobile open toggle aria-expanded = %q, want %q", state.ToggleExpanded, "true")
	}
	if state.OverlayHidden {
		t.Fatalf("mobile open overlayHidden = %t, want false", state.OverlayHidden)
	}
	if got := state.BodyAttrs["data-sidebar-open"]; got != "true" {
		t.Fatalf("mobile open body attrs = %#v, want data-sidebar-open=true", state.BodyAttrs)
	}

	if !mobile.clickSidebarClose() {
		t.Fatal("clickSidebarClose() = false, want true")
	}

	state = mobile.sidebarState()
	requireClosedState(state, "mobile closed after close button")
	if _, ok := state.BodyAttrs["data-sidebar-open"]; ok {
		t.Fatalf("mobile closed body attrs = %#v, want data-sidebar-open removed", state.BodyAttrs)
	}

	desktop := newSidebarRuntimeHarness(t, html, nil, false, false)
	state = desktop.sidebarState()
	if state.ToggleHidden {
		t.Fatalf("desktop toggleHidden = %t, want false after sidebar init", state.ToggleHidden)
	}
	if state.ShellHidden {
		t.Fatalf("desktop shellHidden = %t, want false for always-visible sidebar", state.ShellHidden)
	}
	if _, ok := state.ShellAttrs["aria-hidden"]; ok {
		t.Fatalf("desktop shell attrs = %#v, want aria-hidden removed", state.ShellAttrs)
	}
	if _, ok := state.ShellAttrs["inert"]; ok {
		t.Fatalf("desktop shell attrs = %#v, want inert removed", state.ShellAttrs)
	}
	if state.ToggleExpanded != "false" {
		t.Fatalf("desktop toggle aria-expanded = %q, want %q", state.ToggleExpanded, "false")
	}
	if !state.OverlayHidden {
		t.Fatalf("desktop overlayHidden = %t, want true", state.OverlayHidden)
	}
}

func TestSidebarRuntimeNoteLinksParticipateInPopoverBinding(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	html := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			Language: "en",
			Sidebar:  model.SidebarConfig{Enabled: true},
			Popover:  model.PopoverConfig{Enabled: true},
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
		SidebarTree: []model.SidebarNode{{
			Name:  "notes",
			URL:   "notes/",
			IsDir: true,
			Children: []model.SidebarNode{
				{
					Name:     "Guide",
					URL:      "guide/",
					IsActive: true,
				},
				{
					Name: "Reference",
					URL:  "reference/",
				},
			},
		}},
	})

	harness := newSidebarRuntimeHarness(t, html, nil, true, false)

	guide := harness.linkSnapshot(t, "Guide")
	if guide == nil {
		t.Fatal("Guide link snapshot = nil, want sidebar note link")
	}
	if got := guide.Attrs["data-popover-path"]; got != "guide" {
		t.Fatalf("Guide data-popover-path = %q, want %q", got, "guide")
	}
	if !containsAllStrings(guide.ListenerTypes, "focusin", "focusout", "mouseenter", "mouseleave") {
		t.Fatalf("Guide popover listener types = %#v, want standard popover bindings", guide.ListenerTypes)
	}

	reference := harness.linkSnapshot(t, "Reference")
	if reference == nil {
		t.Fatal("Reference link snapshot = nil, want sidebar note link")
	}
	if got := reference.Attrs["data-popover-path"]; got != "reference" {
		t.Fatalf("Reference data-popover-path = %q, want %q", got, "reference")
	}

	dir := harness.linkSnapshot(t, "notes")
	if dir == nil {
		t.Fatal("notes link snapshot = nil, want sidebar directory link")
	}
	if _, ok := dir.Attrs["data-popover-path"]; ok {
		t.Fatalf("notes directory link attrs = %#v, want no popover metadata on folder links", dir.Attrs)
	}
	if len(dir.ListenerTypes) != 0 {
		t.Fatalf("notes directory listener types = %#v, want no popover bindings", dir.ListenerTypes)
	}
}

func TestSidebarRuntimeNoteLinksSkipPopoverMetadataWhenDisabled(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	html := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			Language: "en",
			Sidebar:  model.SidebarConfig{Enabled: true},
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
		SidebarTree: []model.SidebarNode{{
			Name:  "notes",
			URL:   "notes/",
			IsDir: true,
			Children: []model.SidebarNode{
				{
					Name:     "Guide",
					URL:      "guide/",
					IsActive: true,
				},
				{
					Name: "Reference",
					URL:  "reference/",
				},
			},
		}},
	})

	harness := newSidebarRuntimeHarness(t, html, nil, false, false)

	for _, label := range []string{"Guide", "Reference"} {
		link := harness.linkSnapshot(t, label)
		if link == nil {
			t.Fatalf("%s link snapshot = nil, want sidebar note link", label)
		}
		if got, ok := link.Attrs["data-popover-path"]; ok {
			t.Fatalf("%s data-popover-path = %q, want missing attribute when popover is disabled", label, got)
		}
		if len(link.ListenerTypes) != 0 {
			t.Fatalf("%s listener types = %#v, want no popover bindings when popover is disabled", label, link.ListenerTypes)
		}
	}
}

func newSidebarRuntimeHarness(t *testing.T, html string, storage map[string]string, includePopover bool, mobile bool) *sidebarRuntimeHarness {
	t.Helper()

	fixture, sidebarScript, popoverScript := buildSidebarRuntimeFixture(t, html, storage, includePopover, mobile)
	fixtureJSON, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("json.Marshal(sidebar runtime fixture) error = %v", err)
	}

	vm := goja.New()
	if _, err := vm.RunString(sidebarRuntimePrelude); err != nil {
		t.Fatalf("RunString(sidebar runtime prelude) error = %v", err)
	}
	if _, err := vm.RunString("var __fixture = JSON.parse(" + jsStringLiteral(t, string(fixtureJSON)) + ");\n__sidebarTestSetup(__fixture);"); err != nil {
		t.Fatalf("RunString(sidebar runtime fixture) error = %v", err)
	}
	if _, err := vm.RunString(sidebarScript); err != nil {
		t.Fatalf("RunString(sidebar script) error = %v", err)
	}
	if includePopover {
		if _, err := vm.RunString(popoverScript); err != nil {
			t.Fatalf("RunString(popover script) error = %v", err)
		}
	}

	return &sidebarRuntimeHarness{t: t, vm: vm}
}

func (h *sidebarRuntimeHarness) sidebarReady() string {
	h.t.Helper()

	var ready string
	h.export(`__sidebarTest.sidebarReady()`, &ready)
	return ready
}

func (h *sidebarRuntimeHarness) sidebarState() sidebarStateSnapshot {
	h.t.Helper()

	var state sidebarStateSnapshot
	h.export(`__sidebarTest.sidebarState()`, &state)
	return state
}

func (h *sidebarRuntimeHarness) sidebarTree() []sidebarRenderedItem {
	h.t.Helper()

	var tree []sidebarRenderedItem
	h.export(`__sidebarTest.sidebarTree()`, &tree)
	return tree
}

func (h *sidebarRuntimeHarness) clickSidebarToggle() bool {
	h.t.Helper()

	var ok bool
	h.export(`__sidebarTest.clickSidebarToggle()`, &ok)
	return ok
}

func (h *sidebarRuntimeHarness) clickSidebarClose() bool {
	h.t.Helper()

	var ok bool
	h.export(`__sidebarTest.clickSidebarClose()`, &ok)
	return ok
}

func (h *sidebarRuntimeHarness) clickToggleFor(label string) bool {
	h.t.Helper()

	var ok bool
	h.export(`__sidebarTest.clickToggleFor(`+jsStringLiteral(h.t, label)+`)`, &ok)
	return ok
}

func (h *sidebarRuntimeHarness) storage() map[string]string {
	h.t.Helper()

	var storage map[string]string
	h.export(`__sidebarTest.storage()`, &storage)
	return storage
}

func (h *sidebarRuntimeHarness) linkSnapshot(t *testing.T, label string) *sidebarLinkSnapshot {
	t.Helper()

	value, err := h.vm.RunString(`__sidebarTest.linkSnapshot(` + jsStringLiteral(t, label) + `)`)
	if err != nil {
		t.Fatalf("RunString(linkSnapshot) error = %v", err)
	}
	if goja.IsNull(value) || goja.IsUndefined(value) {
		return nil
	}

	var snapshot sidebarLinkSnapshot
	exportGojaValue(t, value, &snapshot)
	return &snapshot
}

func (h *sidebarRuntimeHarness) export(expr string, dest any) {
	h.t.Helper()

	value, err := h.vm.RunString(expr)
	if err != nil {
		h.t.Fatalf("RunString(%q) error = %v", expr, err)
	}
	exportGojaValue(h.t, value, dest)
}

func buildSidebarRuntimeFixture(t *testing.T, html string, storage map[string]string, includePopover bool, mobile bool) (sidebarRuntimeFixture, string, string) {
	t.Helper()

	doc := parseSidebarRuntimeHTML(t, html)
	fixture := sidebarRuntimeFixture{
		SidebarJSON:  findScriptTextByID(t, doc, "sidebar-data"),
		SiteRootRel:  findElementAttr(t, doc, "nav", "data-sidebar-root", "data-site-root-rel"),
		BaseURI:      "https://example.com/blog/current/index.html",
		LocalStorage: cloneStringMap(storage),
		Mobile:       mobile,
	}
	if fixture.SiteRootRel == "" {
		fixture.SiteRootRel = "./"
	}

	sidebarScript := findInlineScriptContaining(t, doc, "obsite.sidebar.expanded.v1")
	popoverScript := ""
	if includePopover {
		fixture.PopoverEnabled = true
		fixture.PopoverRoot = findElementAttr(t, doc, "aside", "data-popover-card", "data-popover-root")
		popoverScript = findInlineScriptContaining(t, doc, `document.querySelector("[data-popover-card]")`)
		if fixture.PopoverRoot == "" {
			t.Fatal("popover root attribute missing from rendered template output")
		}
	}

	return fixture, sidebarScript, popoverScript
}

func parseSidebarRuntimeHTML(t *testing.T, html string) *xhtml.Node {
	t.Helper()

	doc, err := xhtml.Parse(strings.NewReader(html))
	if err != nil {
		t.Fatalf("xhtml.Parse() error = %v", err)
	}
	return doc
}

func findScriptTextByID(t *testing.T, node *xhtml.Node, id string) string {
	t.Helper()

	match := findHTMLNode(node, func(candidate *xhtml.Node) bool {
		return candidate.Type == xhtml.ElementNode && candidate.Data == "script" && htmlAttrValue(candidate, "id") == id
	})
	if match == nil {
		t.Fatalf("script#%s not found in rendered template output", id)
	}
	return htmlNodeText(match)
}

func findInlineScriptContaining(t *testing.T, node *xhtml.Node, needle string) string {
	t.Helper()

	match := findHTMLNode(node, func(candidate *xhtml.Node) bool {
		return candidate.Type == xhtml.ElementNode && candidate.Data == "script" && strings.Contains(htmlNodeText(candidate), needle)
	})
	if match == nil {
		t.Fatalf("inline script containing %q not found in rendered template output", needle)
	}
	return htmlNodeText(match)
}

func findElementAttr(t *testing.T, node *xhtml.Node, tag string, markerAttr string, targetAttr string) string {
	t.Helper()

	match := findHTMLNode(node, func(candidate *xhtml.Node) bool {
		return candidate.Type == xhtml.ElementNode && candidate.Data == tag && htmlAttrPresent(candidate, markerAttr)
	})
	if match == nil {
		t.Fatalf("%s[%s] not found in rendered template output", tag, markerAttr)
	}
	return htmlAttrValue(match, targetAttr)
}

func findHTMLNode(node *xhtml.Node, match func(*xhtml.Node) bool) *xhtml.Node {
	if node == nil {
		return nil
	}
	if match(node) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findHTMLNode(child, match); found != nil {
			return found
		}
	}
	return nil
}

func htmlAttrPresent(node *xhtml.Node, key string) bool {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return true
		}
	}
	return false
}

func htmlAttrValue(node *xhtml.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func htmlNodeText(node *xhtml.Node) string {
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.TextNode {
			builder.WriteString(child.Data)
		}
	}
	return builder.String()
}

func jsStringLiteral(t *testing.T, value string) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(js string literal) error = %v", err)
	}
	return string(encoded)
}

func exportGojaValue(t *testing.T, value goja.Value, dest any) {
	t.Helper()

	encoded, err := json.Marshal(value.Export())
	if err != nil {
		t.Fatalf("json.Marshal(goja export) error = %v", err)
	}
	if err := json.Unmarshal(encoded, dest); err != nil {
		t.Fatalf("json.Unmarshal(goja export) error = %v", err)
	}
}

func requireSidebarItem(t *testing.T, items []sidebarRenderedItem, label string) *sidebarRenderedItem {
	t.Helper()

	item := findSidebarItem(items, label)
	if item == nil {
		t.Fatalf("sidebar item %q not found in %#v", label, items)
	}
	return item
}

func findSidebarItem(items []sidebarRenderedItem, label string) *sidebarRenderedItem {
	for index := range items {
		if items[index].Label == label {
			return &items[index]
		}
		if found := findSidebarItem(items[index].Children, label); found != nil {
			return found
		}
	}
	return nil
}

func containsAllStrings(got []string, want ...string) bool {
	for _, candidate := range want {
		matched := false
		for _, actual := range got {
			if actual == candidate {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
