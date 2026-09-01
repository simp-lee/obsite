package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

const sharedSidebarPrelude = `
var window = this;
var fixture = JSON.parse(__fixtureJSON);
function Element(tag) {
  this.tagName = String(tag || "").toUpperCase();
  this.children = [];
  this.parentNode = null;
  this.attributes = {};
  this.listeners = {};
  this.className = "";
  this.textContent = "";
  this.hidden = false;
  this.style = {};
  this.id = "";
  this.offsetWidth = 240;
  this.offsetHeight = 120;
  var self = this;
  this.classList = {
    add: function (name) {
      if (!(" " + self.className + " ").includes(" " + name + " ")) self.className = (self.className + " " + name).trim();
    },
    contains: function (name) { return (" " + self.className + " ").includes(" " + name + " "); }
  };
}
Element.prototype.appendChild = function (child) { child.parentNode = this; this.children.push(child); return child; };
Element.prototype.setAttribute = function (name, value) { this.attributes[name] = String(value); if (name === "id") this.id = String(value); };
Element.prototype.getAttribute = function (name) { return Object.prototype.hasOwnProperty.call(this.attributes, name) ? this.attributes[name] : null; };
Element.prototype.hasAttribute = function (name) { return Object.prototype.hasOwnProperty.call(this.attributes, name); };
Element.prototype.removeAttribute = function (name) { delete this.attributes[name]; };
Element.prototype.addEventListener = function (name, listener) { (this.listeners[name] || (this.listeners[name] = [])).push(listener); };
Element.prototype.dispatch = function (name, event) { var list = this.listeners[name] || []; for (var i = 0; i < list.length; i++) list[i](event || {target: this}); };
Element.prototype.querySelector = function (selector) {
  if (selector === ":scope > ul") {
    for (var i = 0; i < this.children.length; i++) if (this.children[i].tagName === "UL") return this.children[i];
    return null;
  }
  if (selector === "svg") return findByTag(this, "SVG");
  return null;
};
Element.prototype.closest = function (selector) {
  var current = this;
  while (current) {
    if (selector === "a.sidebar-link" && current.tagName === "A" && current.classList.contains("sidebar-link")) return current;
    if (selector === "[data-popover-path]" && current.hasAttribute("data-popover-path")) return current;
    current = current.parentNode;
  }
  return null;
};
Element.prototype.getBoundingClientRect = function () { return {left: 20, top: 20, bottom: 40}; };
Element.prototype.contains = function (candidate) { var current = candidate; while (current) { if (current === this) return true; current = current.parentNode; } return false; };
function findByTag(node, tag) {
  for (var i = 0; i < node.children.length; i++) {
    if (node.children[i].tagName === tag) return node.children[i];
    var nested = findByTag(node.children[i], tag); if (nested) return nested;
  }
  return null;
}
function findByText(node, tag, text) {
  if (node.tagName === tag && node.textContent === text) return node;
  for (var i = 0; i < node.children.length; i++) { var found = findByText(node.children[i], tag, text); if (found) return found; }
  return null;
}
function directChild(node, tag) { for (var i = 0; i < node.children.length; i++) if (node.children[i].tagName === tag) return node.children[i]; return null; }
function URL(relative, base) {
  relative = String(relative); base = base && base.href ? base.href : String(base || "");
  if (/^https?:\/\//.test(relative)) { this.href = relative; return; }
  var match = /^(https?:\/\/[^/]+)(\/.*)?$/.exec(base); var origin = match ? match[1] : ""; var pathname = match && match[2] ? match[2] : "/";
  var directory = pathname.endsWith("/") ? pathname : pathname.slice(0, pathname.lastIndexOf("/") + 1);
  var parts = (relative.startsWith("/") ? relative : directory + relative).split("/"); var clean = [];
  for (var i = 0; i < parts.length; i++) { if (!parts[i] || parts[i] === ".") continue; if (parts[i] === "..") clean.pop(); else clean.push(parts[i]); }
  this.pathname = "/" + clean.join("/") + (relative.endsWith("/") ? "/" : "");
  this.href = origin + this.pathname;
}
window.URL = URL;
var root = new Element("html");
root.setAttribute("data-obsite-base-path", fixture.basePath || "/blog/");
root.setAttribute("data-obsite-sidebar", "");
if (fixture.popover) root.setAttribute("data-obsite-popover", "");
var runtimeScript = new Element("script"); runtimeScript.src = fixture.runtimeScriptURL || "https://example.test/blog/assets/obsite/runtime.test.js";
var shell = new Element("aside"); shell.hidden = true;
var sidebarRoot = new Element("nav");
var siteBody = new Element("div");
var toggle = new Element("button"); toggle.hidden = true; toggle.setAttribute("aria-expanded", "false");
var closeButton = new Element("button");
var overlay = new Element("div"); overlay.hidden = true;
var body = new Element("body");
var head = new Element("head");
var popoverCard = new Element("aside"); popoverCard.id = "obsite-popover-card"; popoverCard.hidden = true; popoverCard.setAttribute("aria-hidden", "true");
var staticLink = new Element("a"); staticLink.textContent = "Static Link"; staticLink.setAttribute("data-popover-path", "reference"); staticLink.setAttribute("aria-describedby", "authored-help");
var documentListeners = {};
var document = {
  documentElement: root, currentScript: runtimeScript, readyState: "complete", body: body, head: head, baseURI: "https://example.test/blog/guide/",
  querySelector: function (selector) {
    return {"[data-theme-toggle]": null,"[data-sidebar-shell]": shell,"[data-sidebar-root]": sidebarRoot,"[data-site-body]": siteBody,"[data-sidebar-toggle]": toggle,"[data-sidebar-close]": closeButton,"[data-sidebar-overlay]": overlay,"[data-popover-card]": fixture.popover ? popoverCard : null,"[data-page-content]": null}[selector] || null;
  },
  querySelectorAll: function () { return []; },
  createElement: function (tag) { return new Element(tag); },
  addEventListener: function (name, listener) { (documentListeners[name] || (documentListeners[name] = [])).push(listener); }
};
window.document = document;
window.location = {pathname: fixture.pathname || "/blog/guide/"};
var media = {matches: !!fixture.mobile, listeners: [], addEventListener: function (_, listener) { this.listeners.push(listener); }, addListener: function (listener) { this.listeners.push(listener); }};
window.matchMedia = function () { return media; };
var windowListeners = {};
window.addEventListener = function (name, listener) { (windowListeners[name] || (windowListeners[name] = [])).push(listener); };
window.innerWidth = 1280; window.innerHeight = 800;
var timers = {}; var nextTimer = 1;
window.setTimeout = function (callback, delay) { var id = nextTimer++; if (delay >= 150) callback(); else timers[id] = callback; return id; };
window.clearTimeout = function (id) { delete timers[id]; };
function flushTimers() { var pending = timers; timers = {}; Object.keys(pending).forEach(function (id) { pending[id](); }); };
var storage = fixture.storage || {};
window.localStorage = {getItem: function (key) { return Object.prototype.hasOwnProperty.call(storage, key) ? storage[key] : null; }, setItem: function (key, value) { storage[key] = String(value); }};
var logs = [];
window.console = {error: function (message, error) { logs.push(String(message) + " " + String(error || "")); }, warn: function (message, error) { logs.push(String(message) + " " + String(error || "")); }};
var fetchCalls = [];
window.fetch = function (url, options) {
  url = String(url); fetchCalls.push({url: url, cache: options && options.cache});
  if (url.indexOf("/_popover/") !== -1) {
    if (fixture.failPopover) return Promise.reject(new Error("popover offline"));
    return Promise.resolve({ok: true, status: 200, json: function () { return Promise.resolve(fixture.popoverPayload || {title: "Reference", summary: "Preview summary", tags: ["field"]}); }});
  }
  if (fixture.failFetch) return Promise.reject(new Error("offline"));
  return Promise.resolve({ok: true, status: 200, json: function () { return Promise.resolve(fixture.nodes); }});
};
function findItem(label) { var link = findByText(sidebarRoot, "A", label); return link && link.parentNode ? link.parentNode.parentNode : null; }
window.__sidebarSharedTest = {
  snapshot: function () {
    var notes = findItem("notes"); var guide = findByText(sidebarRoot, "A", "Guide"); var branch = notes ? directChild(notes, "UL") : null;
    var garden = findByText(sidebarRoot, "A", "garden"); var gardenItem = garden && garden.parentNode ? garden.parentNode.parentNode : null;
    return {
      fetchCalls: fetchCalls, logs: logs, ready: siteBody.getAttribute("data-sidebar-ready"), shellHidden: shell.hidden,
      notesExpanded: notes ? notes.getAttribute("data-expanded") : null, branchHidden: branch ? branch.hidden : null,
      guideCurrent: guide ? guide.getAttribute("aria-current") : null, guideHref: guide ? guide.href : null,
      gardenCurrent: garden ? garden.getAttribute("aria-current") : null, gardenExpanded: gardenItem ? gardenItem.getAttribute("data-expanded") : null,
      toggleHidden: toggle.hidden, toggleExpanded: toggle.getAttribute("aria-expanded"), overlayHidden: overlay.hidden,
      shellAriaHidden: shell.getAttribute("aria-hidden"), shellInert: shell.hasAttribute("inert"), bodyOpen: body.getAttribute("data-sidebar-open"), storage: storage,
      popoverHidden: popoverCard.hidden, popoverAriaHidden: popoverCard.getAttribute("aria-hidden"), popoverText: popoverCard.children.map(function (child) { return child.textContent; }).join("|"),
      staticDescribedBy: staticLink.getAttribute("aria-describedby"), staticPopoverPath: staticLink.getAttribute("data-popover-path"), sidebarDescribedBy: findByText(sidebarRoot, "A", "Reference") ? findByText(sidebarRoot, "A", "Reference").getAttribute("aria-describedby") : null
    };
  },
  clickBranch: function (label) { var item = findItem(label); var row = item && directChild(item, "DIV"); var button = row && directChild(row, "BUTTON"); if (!button) return false; button.dispatch("click"); return true; },
  clickLaunch: function () { toggle.dispatch("click"); },
  clickOverlay: function () { overlay.dispatch("click"); },
  hoverStatic: function () { var list = documentListeners.mouseover || []; for (var i = 0; i < list.length; i++) list[i]({target: staticLink, relatedTarget: null}); },
  leaveStatic: function (relatedTarget) { var list = documentListeners.mouseout || []; for (var i = 0; i < list.length; i++) list[i]({target: staticLink, relatedTarget: relatedTarget || null}); },
  focusStatic: function () { var list = documentListeners.focusin || []; for (var i = 0; i < list.length; i++) list[i]({target: staticLink, relatedTarget: null}); },
  blurStatic: function () { var list = documentListeners.focusout || []; for (var i = 0; i < list.length; i++) list[i]({target: staticLink, relatedTarget: null}); },
  enterCard: function () { popoverCard.dispatch("mouseenter", {target: popoverCard}); },
  leaveCard: function () { popoverCard.dispatch("mouseleave", {target: popoverCard}); },
  flushTimers: function () { flushTimers(); },
  hoverSidebar: function (label) { var link = findByText(sidebarRoot, "A", label); var list = documentListeners.mouseover || []; for (var i = 0; i < list.length; i++) list[i]({target: link, relatedTarget: null}); },
  focusSidebar: function (label) { var link = findByText(sidebarRoot, "A", label); var out = documentListeners.focusout || []; for (var i = 0; i < out.length; i++) out[i]({target: staticLink, relatedTarget: link}); var input = documentListeners.focusin || []; for (var j = 0; j < input.length; j++) input[j]({target: link, relatedTarget: staticLink}); },
  blurSidebar: function (label) { var link = findByText(sidebarRoot, "A", label); var list = documentListeners.focusout || []; for (var i = 0; i < list.length; i++) list[i]({target: link, relatedTarget: null}); },
  escape: function () { var list = documentListeners.keydown || []; for (var i = 0; i < list.length; i++) list[i]({key: "Escape"}); }
};
`

type sharedSidebarSnapshot struct {
	FetchCalls []struct {
		URL   string `json:"url"`
		Cache string `json:"cache"`
	} `json:"fetchCalls"`
	Logs               []string          `json:"logs"`
	Ready              string            `json:"ready"`
	ShellHidden        bool              `json:"shellHidden"`
	NotesExpanded      string            `json:"notesExpanded"`
	BranchHidden       bool              `json:"branchHidden"`
	GuideCurrent       string            `json:"guideCurrent"`
	GuideHref          string            `json:"guideHref"`
	GardenCurrent      string            `json:"gardenCurrent"`
	GardenExpanded     string            `json:"gardenExpanded"`
	ToggleHidden       bool              `json:"toggleHidden"`
	ToggleExpanded     string            `json:"toggleExpanded"`
	OverlayHidden      bool              `json:"overlayHidden"`
	ShellAriaHidden    string            `json:"shellAriaHidden"`
	ShellInert         bool              `json:"shellInert"`
	BodyOpen           string            `json:"bodyOpen"`
	Storage            map[string]string `json:"storage"`
	PopoverHidden      bool              `json:"popoverHidden"`
	PopoverAriaHidden  string            `json:"popoverAriaHidden"`
	PopoverText        string            `json:"popoverText"`
	StaticDescribedBy  string            `json:"staticDescribedBy"`
	StaticPopoverPath  string            `json:"staticPopoverPath"`
	SidebarDescribedBy string            `json:"sidebarDescribedBy"`
}

func TestSharedRuntimeFetchesSidebarAndComputesCurrentAncestors(t *testing.T) {
	t.Parallel()
	vm := runSharedSidebarRuntime(t, map[string]any{
		"basePath": "/blog/", "pathname": "/blog/guide/",
		"nodes": []map[string]any{{"name": "notes", "url": "notes/", "isDir": true, "children": []map[string]any{{"name": "Guide", "url": "guide/", "isDir": false}}}},
	})
	state := sharedSidebarState(t, vm)
	if len(state.FetchCalls) != 1 || state.FetchCalls[0].URL != "https://example.test/blog/assets/obsite/sidebar.json" || state.FetchCalls[0].Cache != "no-cache" {
		t.Fatalf("fetch calls = %#v, want one revalidating sidebar.json request", state.FetchCalls)
	}
	if state.Ready != "true" || state.ShellHidden || state.NotesExpanded != "true" || state.BranchHidden || state.GuideCurrent != "page" || state.GuideHref != "https://example.test/blog/guide/" {
		t.Fatalf("Sidebar state = %#v, want rendered active branch", state)
	}
	if _, err := vm.RunString(`__sidebarSharedTest.clickBranch("notes")`); err != nil {
		t.Fatal(err)
	}
	state = sharedSidebarState(t, vm)
	if state.NotesExpanded != "false" || !state.BranchHidden || !strings.Contains(state.Storage["obsite.sidebar.expanded.v1:/blog/"], `"notes":false`) {
		t.Fatalf("collapsed Sidebar state = %#v", state)
	}
}

func TestSharedRuntimeSidebarUsesServedRootDuringSubpathPreview(t *testing.T) {
	t.Parallel()
	vm := runSharedSidebarRuntime(t, map[string]any{
		"basePath": "/blog/", "pathname": "/guide/", "runtimeScriptURL": "https://example.test/assets/obsite/runtime.test.js",
		"nodes": []map[string]any{{"name": "Guide", "url": "guide/", "isDir": false}},
	})
	state := sharedSidebarState(t, vm)
	if len(state.FetchCalls) != 1 || state.FetchCalls[0].URL != "https://example.test/assets/obsite/sidebar.json" {
		t.Fatalf("preview fetch calls = %#v", state.FetchCalls)
	}
	if state.GuideCurrent != "page" || state.GuideHref != "https://example.test/guide/" {
		t.Fatalf("preview Sidebar state = %#v, want links rooted at preview mount", state)
	}
}

func TestSharedRuntimeSidebarMarksPaginatedFolderAndAncestors(t *testing.T) {
	t.Parallel()
	vm := runSharedSidebarRuntime(t, map[string]any{
		"basePath": "/blog/", "pathname": "/blog/notes/garden/page/2/",
		"nodes": []map[string]any{{"name": "notes", "url": "notes/", "isDir": true, "children": []map[string]any{{"name": "garden", "url": "notes/garden/", "isDir": true, "children": []map[string]any{{"name": "Guide", "url": "guide/", "isDir": false}}}}}},
	})
	state := sharedSidebarState(t, vm)
	if state.GardenCurrent != "page" || state.GardenExpanded != "true" || state.NotesExpanded != "true" {
		t.Fatalf("paginated folder Sidebar state = %#v", state)
	}
}

func TestSharedRuntimeSidebarMobileAccessibilityAndEscape(t *testing.T) {
	t.Parallel()
	vm := runSharedSidebarRuntime(t, map[string]any{"basePath": "/blog/", "pathname": "/blog/guide/", "mobile": true, "nodes": []map[string]any{{"name": "Guide", "url": "guide/", "isDir": false}}})
	state := sharedSidebarState(t, vm)
	if state.ToggleHidden || state.ShellAriaHidden != "true" || !state.ShellInert || !state.OverlayHidden {
		t.Fatalf("initial mobile Sidebar state = %#v", state)
	}
	if _, err := vm.RunString(`__sidebarSharedTest.clickLaunch()`); err != nil {
		t.Fatal(err)
	}
	state = sharedSidebarState(t, vm)
	if state.ToggleExpanded != "true" || state.OverlayHidden || state.ShellAriaHidden != "" || state.ShellInert || state.BodyOpen != "true" {
		t.Fatalf("open mobile Sidebar state = %#v", state)
	}
	if _, err := vm.RunString(`__sidebarSharedTest.clickOverlay()`); err != nil {
		t.Fatal(err)
	}
	state = sharedSidebarState(t, vm)
	if state.ToggleExpanded != "false" || !state.OverlayHidden || state.ShellAriaHidden != "true" || !state.ShellInert || state.BodyOpen != "" {
		t.Fatalf("overlay-closed mobile Sidebar state = %#v", state)
	}
	if _, err := vm.RunString(`__sidebarSharedTest.clickLaunch(); __sidebarSharedTest.escape()`); err != nil {
		t.Fatal(err)
	}
	state = sharedSidebarState(t, vm)
	if state.ToggleExpanded != "false" || !state.OverlayHidden || state.ShellAriaHidden != "true" || !state.ShellInert || state.BodyOpen != "" {
		t.Fatalf("Escape-closed mobile Sidebar state = %#v", state)
	}
}

func TestSharedRuntimePopoverDelegatesToStaticAndAsyncSidebarLinks(t *testing.T) {
	t.Parallel()
	vm := runSharedSidebarRuntime(t, map[string]any{
		"basePath": "/blog/", "pathname": "/blog/guide/", "popover": true,
		"nodes": []map[string]any{{"name": "Reference", "url": "reference/", "isDir": false}},
	})
	if _, err := vm.RunString(`__sidebarSharedTest.focusStatic()`); err != nil {
		t.Fatal(err)
	}
	state := sharedSidebarState(t, vm)
	if state.PopoverHidden || state.PopoverAriaHidden != "false" || !strings.Contains(state.PopoverText, "Reference|Preview summary") || state.StaticDescribedBy != "authored-help obsite-popover-card" {
		t.Fatalf("focused static-link Popover state = %#v", state)
	}
	if _, err := vm.RunString(`__sidebarSharedTest.leaveStatic(); __sidebarSharedTest.flushTimers()`); err != nil {
		t.Fatal(err)
	}
	if state = sharedSidebarState(t, vm); state.PopoverHidden {
		t.Fatalf("focused Popover closed on pointer exit: %#v", state)
	}
	if _, err := vm.RunString(`__sidebarSharedTest.escape(); __sidebarSharedTest.hoverStatic(); __sidebarSharedTest.leaveStatic(); __sidebarSharedTest.enterCard(); __sidebarSharedTest.flushTimers()`); err != nil {
		t.Fatal(err)
	}
	if state = sharedSidebarState(t, vm); state.PopoverHidden {
		t.Fatalf("hovered card did not cancel hide: %#v", state)
	}
	if _, err := vm.RunString(`__sidebarSharedTest.leaveCard(); __sidebarSharedTest.flushTimers(); __sidebarSharedTest.hoverStatic(); __sidebarSharedTest.hoverSidebar("Reference")`); err != nil {
		t.Fatal(err)
	}
	state = sharedSidebarState(t, vm)
	if state.PopoverHidden || state.StaticDescribedBy != "authored-help" || state.SidebarDescribedBy != "obsite-popover-card" {
		t.Fatalf("async Sidebar-link Popover switch state = %#v", state)
	}
	popoverRequests := 0
	for _, call := range state.FetchCalls {
		if strings.Contains(call.URL, "/_popover/") {
			popoverRequests++
			if call.URL != "https://example.test/blog/_popover/reference.json" {
				t.Fatalf("Popover URL = %q", call.URL)
			}
		}
	}
	if popoverRequests != 1 {
		t.Fatalf("Popover request count = %d, want cached single request; calls=%#v", popoverRequests, state.FetchCalls)
	}
}

func TestSharedRuntimePopoverClearsCardHoverOnKeyboardTargetSwitch(t *testing.T) {
	t.Parallel()
	vm := runSharedSidebarRuntime(t, map[string]any{
		"basePath": "/blog/", "pathname": "/blog/guide/", "popover": true,
		"nodes": []map[string]any{{"name": "Reference", "url": "reference/", "isDir": false}},
	})
	if _, err := vm.RunString(`__sidebarSharedTest.focusStatic(); __sidebarSharedTest.enterCard(); __sidebarSharedTest.focusSidebar("Reference"); __sidebarSharedTest.blurSidebar("Reference"); __sidebarSharedTest.flushTimers()`); err != nil {
		t.Fatal(err)
	}
	state := sharedSidebarState(t, vm)
	if !state.PopoverHidden || state.StaticDescribedBy != "authored-help" || state.SidebarDescribedBy != "" {
		t.Fatalf("keyboard-switched Popover state = %#v", state)
	}
}

func TestSharedRuntimePopoverFailureDoesNotRetryOrAlterLink(t *testing.T) {
	t.Parallel()
	vm := runSharedSidebarRuntime(t, map[string]any{"basePath": "/blog/", "pathname": "/blog/", "popover": true, "failPopover": true, "nodes": []any{}})
	if _, err := vm.RunString(`__sidebarSharedTest.hoverStatic(); __sidebarSharedTest.hoverStatic()`); err != nil {
		t.Fatal(err)
	}
	state := sharedSidebarState(t, vm)
	popoverRequests := 0
	for _, call := range state.FetchCalls {
		if strings.Contains(call.URL, "/_popover/") {
			popoverRequests++
		}
	}
	if popoverRequests != 1 || !state.PopoverHidden || state.StaticDescribedBy != "authored-help" || state.StaticPopoverPath != "reference" {
		t.Fatalf("failed Popover state = %#v", state)
	}
	var diagnostics int
	for _, message := range state.Logs {
		if strings.Contains(message, "Popover request failed") {
			diagnostics++
		}
	}
	if diagnostics != 1 {
		t.Fatalf("Popover diagnostics = %d, logs=%#v", diagnostics, state.Logs)
	}
}

func TestSharedRuntimePopoverDisabledMakesNoRequest(t *testing.T) {
	t.Parallel()
	vm := runSharedSidebarRuntime(t, map[string]any{"basePath": "/blog/", "pathname": "/blog/", "nodes": []any{}})
	if _, err := vm.RunString(`__sidebarSharedTest.hoverStatic()`); err != nil {
		t.Fatal(err)
	}
	state := sharedSidebarState(t, vm)
	for _, call := range state.FetchCalls {
		if strings.Contains(call.URL, "/_popover/") {
			t.Fatalf("disabled Popover requested %q", call.URL)
		}
	}
	if state.StaticDescribedBy != "authored-help" {
		t.Fatalf("disabled Popover changed authored description: %#v", state)
	}
}

func TestSharedRuntimeSidebarFailureLeavesMountHiddenAndLogsOnce(t *testing.T) {
	t.Parallel()
	vm := runSharedSidebarRuntime(t, map[string]any{"basePath": "/blog/", "pathname": "/blog/", "failFetch": true, "nodes": []any{}})
	state := sharedSidebarState(t, vm)
	if len(state.FetchCalls) != 1 || len(state.Logs) != 1 || !strings.Contains(state.Logs[0], "Sidebar initialization failed") || !state.ShellHidden || state.Ready != "" {
		t.Fatalf("failed Sidebar state = %#v", state)
	}
}

func runSharedSidebarRuntime(t *testing.T, fixture map[string]any) *goja.Runtime {
	t.Helper()
	fixtureJSON, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	vm := goja.New()
	if err := vm.Set("__fixtureJSON", string(fixtureJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(sharedSidebarPrelude); err != nil {
		t.Fatalf("run Sidebar prelude: %v", err)
	}
	if _, err := vm.RunString(readTemplateAsset(t, "runtime.js")); err != nil {
		t.Fatalf("run shared runtime: %v", err)
	}
	return vm
}

func sharedSidebarState(t *testing.T, vm *goja.Runtime) sharedSidebarSnapshot {
	t.Helper()
	value, err := vm.RunString(`JSON.stringify(__sidebarSharedTest.snapshot())`)
	if err != nil {
		t.Fatal(err)
	}
	var state sharedSidebarSnapshot
	if err := json.Unmarshal([]byte(value.String()), &state); err != nil {
		t.Fatal(err)
	}
	return state
}
