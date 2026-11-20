package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLiveRefreshAssetRoute(t *testing.T) {
	srv := &Server{}
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/live-refresh.js", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /assets/live-refresh.js, got %d", rec.Code)
	}
	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/javascript") {
		t.Fatalf("expected Content-Type text/javascript, got %q", contentType)
	}
	cacheControl := rec.Header().Get("Cache-Control")
	if !strings.Contains(cacheControl, "no-store") {
		t.Fatalf("expected Cache-Control no-store, got %q", cacheControl)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data-live-refresh") {
		t.Fatalf("expected live_refresh.js body to contain 'data-live-refresh', got: %s", body)
	}
}

func TestLiveRefreshScriptNodeVM(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}

	nodeScript := `
const assert = require('assert');

// Read embedded script code from stdin
const fs = require('fs');
const scriptCode = fs.readFileSync(0, 'utf8');

// Mock DOM environment
let currentActiveElement = null;
let currentDialogs = [];
let currentPopovers = [];
let isHidden = false;
let fetchCalls = [];

const listeners = {};
const documentMock = {
  hidden: false,
  get activeElement() { return currentActiveElement; },
  addEventListener: (event, cb) => {
    listeners[event] = listeners[event] || [];
    listeners[event].push(cb);
  },
  dispatchEvent: (event) => {
    const list = listeners[event.type] || [];
    list.forEach(cb => cb(event));
  },
  querySelector: (sel) => {
    if (sel === 'dialog[open]') return currentDialogs.length ? currentDialogs[0] : null;
    if (sel.includes(':popover-open')) return currentPopovers.length ? currentPopovers[0] : null;
    if (sel === '#target-table') return domNodes['#target-table'];
    if (sel === '#target-filters') return domNodes['#target-filters'];
    if (sel === '#topnav') return domNodes['#topnav'];
    if (sel === '#subnav') return domNodes['#subnav'];
    return null;
  },
  querySelectorAll: (sel) => {
    if (sel === '[data-live-refresh]') return liveRefreshElements;
    return [];
  }
};

const domNodes = {
  '#target-table': {
    id: 'target-table',
    childNodes: ['old-table-content'],
    replaceChildren: function(...args) { this.childNodes = args; }
  },
  '#target-filters': {
    id: 'target-filters',
    childNodes: ['old-filters-content'],
    replaceChildren: function(...args) { this.childNodes = args; }
  },
  '#topnav': {
    id: 'topnav',
    childNodes: ['old-topnav'],
    replaceChildren: function(...args) { this.childNodes = args; }
  },
  '#subnav': {
    id: 'subnav',
    childNodes: ['old-subnav'],
    replaceChildren: function(...args) { this.childNodes = args; }
  }
};

const liveRefreshElements = [
  {
    getAttribute: (attr) => {
      if (attr === 'data-live-refresh') return '15';
      if (attr === 'data-live-targets') return '#target-table,#target-filters';
      return null;
    }
  }
];

const windowMock = {
  document: documentMock,
  location: { href: 'https://192.168.10.135:8443/images?show=attention' },
  fetch: (url, opts) => {
    fetchCalls.push({ url, opts });
    return Promise.resolve({
      ok: true,
      text: () => Promise.resolve('<div id="target-table">new-table-content</div><nav id="target-filters">new-filters-content</nav>')
    });
  },
  AbortController: class {
    constructor() { this.signal = {}; }
    abort() {}
  },
  DOMParser: class {
    parseFromString(html) {
      return {
        querySelector: (sel) => {
          if (sel === '#target-table') return { childNodes: ['new-table-content'] };
          if (sel === '#target-filters') return { childNodes: ['new-filters-content'] };
          if (sel === '#topnav') return { childNodes: ['new-topnav-with-badge'] };
          if (sel === '#subnav') return { childNodes: ['new-subnav-with-badge'] };
          return null;
        }
      };
    }
  },
  CustomEvent: class {
    constructor(type) { this.type = type; }
  },
  setInterval: (cb, ms) => { timerCb = cb; timerMs = ms; return 123; },
  clearInterval: (id) => {}
};

let timerCb = null;
let timerMs = null;

// Execute script in mock context
const fn = new Function('window', 'document', 'location', 'fetch', 'DOMParser', 'AbortController', 'CustomEvent', 'setInterval', 'clearInterval', scriptCode);
fn.call(windowMock, windowMock, documentMock, windowMock.location, windowMock.fetch, windowMock.DOMParser, windowMock.AbortController, windowMock.CustomEvent, windowMock.setInterval, windowMock.clearInterval);

assert.strictEqual(timerMs, 15000, 'timer should be initialized with 15000ms');
assert(typeof timerCb === 'function', 'timer callback should be set');
assert(windowMock.OMB && typeof windowMock.OMB.refresh === 'function', 'window.OMB.refresh should be exposed');

// Test 1: Programmatic refresh via window.OMB.refresh()
windowMock.OMB.refresh();
assert.strictEqual(fetchCalls.length, 1, 'refresh() should trigger 1 fetch call');
assert.strictEqual(fetchCalls[0].url, 'https://192.168.10.135:8443/images?show=attention', 'url should preserve query params');

// Wait for promise resolution
setTimeout(() => {
  assert.deepStrictEqual(domNodes['#target-table'].childNodes, ['new-table-content'], 'target-table should be updated');
  assert.deepStrictEqual(domNodes['#target-filters'].childNodes, ['new-filters-content'], 'target-filters should be updated');
  assert.deepStrictEqual(domNodes['#topnav'].childNodes, ['new-topnav-with-badge'], 'topnav should be updated');
  assert.deepStrictEqual(domNodes['#subnav'].childNodes, ['new-subnav-with-badge'], 'subnav should be updated');

  // Test 2: Gating on open dialog
  currentDialogs = [{ open: true }];
  fetchCalls = [];
  timerCb();
  assert.strictEqual(fetchCalls.length, 0, 'fetch should not be called when dialog is open');
  currentDialogs = [];

  // Test 3: Gating on open popover
  currentPopovers = [{ popover: 'auto' }];
  timerCb();
  assert.strictEqual(fetchCalls.length, 0, 'fetch should not be called when popover is open');
  currentPopovers = [];

  // Test 4: Gating on active input focus
  currentActiveElement = { tagName: 'INPUT' };
  timerCb();
  assert.strictEqual(fetchCalls.length, 0, 'fetch should not be called when user is focused on an input');
  currentActiveElement = null;

  // Test 5: Gating on hidden document
  documentMock.hidden = true;
  timerCb();
  assert.strictEqual(fetchCalls.length, 0, 'fetch should not be called when document is hidden');

  // Test 6: Trigger on visibility change
  documentMock.hidden = false;
  listeners['visibilitychange'].forEach(cb => cb());
  assert.strictEqual(fetchCalls.length, 1, 'visibility change should trigger fetch');

  // Let the fetch promise resolve, resetting isFetching = false
  Promise.resolve().then(() => Promise.resolve()).then(() => {
    // Test 7: Trigger on omb:refresh custom event
    fetchCalls = [];
    documentMock.dispatchEvent(new windowMock.CustomEvent('omb:refresh'));
    assert.strictEqual(fetchCalls.length, 1, 'omb:refresh event should trigger fetch');

    console.log('ALL NODE VM TESTS PASSED');
  });
}, 50);
`;

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "node", "-e", nodeScript)
	cmd.Stdin = strings.NewReader(string(liveRefreshJS))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node test failed: %v\nOutput: %s", err, string(out))
	}
	if !strings.Contains(string(out), "ALL NODE VM TESTS PASSED") {
		t.Fatalf("expected test success, got output: %s", string(out))
	}
}
