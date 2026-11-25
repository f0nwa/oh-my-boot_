package control

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDialogBackdropDragOutProtection(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}

	jsPath := filepath.Join("async_block.js")
	src, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("read %s: %v", jsPath, err)
	}

	testScript := `
const assert = require('assert');
const vm = require('vm');

function makeEnv({ withClosedBy }) {
  const docListeners = {};
  const winListeners = {};

  function add(map, type, fn) {
    if (!map[type]) map[type] = [];
    map[type].push(fn);
  }
  function emit(map, type, ev) {
    for (const fn of (map[type] || [])) fn(ev);
  }

  class FakeHTMLDialogElement {
    constructor() {
      this.tagName = 'DIALOG';
      this.nodeName = 'DIALOG';
      this.open = false;
      this.attrs = {};
      this.closeCalls = 0;
      this.cancelDefaultPrevented = false;
      this.rect = { left: 100, top: 100, right: 500, bottom: 400, width: 400, height: 300 };
    }
    hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k); }
    getAttribute(k) { return this.hasAttribute(k) ? this.attrs[k] : null; }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    removeAttribute(k) { delete this.attrs[k]; }
    querySelectorAll() { return []; }
    closest() { return null; }
    getBoundingClientRect() { return this.rect; }
    dispatchEvent(ev) {
      if (ev && ev.type === 'cancel' && this.cancelDefaultPrevented) {
        return false;
      }
      return true;
    }
    showModal() { this.open = true; }
    close() {
      this.closeCalls++;
      this.open = false;
      emit(docListeners, 'close', { target: this });
    }
  }
  if (withClosedBy) {
    Object.defineProperty(FakeHTMLDialogElement.prototype, 'closedBy', {
      get() { return this.attrs.closedby || 'closerequest'; },
      set(v) { this.attrs.closedby = String(v); },
      configurable: true,
    });
  }

  const sandbox = {
    Event: class FakeEvent {
      constructor(type, opts) {
        this.type = type;
        this.cancelable = Boolean(opts && opts.cancelable);
      }
    },
    HTMLDialogElement: FakeHTMLDialogElement,
    PointerEvent: function PointerEvent() {},
    document: {
      readyState: 'complete',
      documentElement: { lang: 'ru' },
      body: {},
      querySelectorAll: () => [],
      getElementById: () => null,
      addEventListener: (type, fn) => add(docListeners, type, fn),
    },
    window: {
      PointerEvent: function PointerEvent() {},
      addEventListener: (type, fn) => add(winListeners, type, fn),
    },
    Set,
    WeakMap,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(` + "`" + strings.ReplaceAll(string(src), "`", "\\`") + "`" + `, sandbox);

  const input = {
    tagName: 'INPUT',
    nodeName: 'INPUT',
    closest: () => null,
  };

  function pointerDown(target, x, y, button = 0) {
    emit(docListeners, 'pointerdown', { target, clientX: x, clientY: y, button });
  }
  function click(target, x, y) {
    emit(docListeners, 'click', {
      target,
      clientX: x,
      clientY: y,
      preventDefault() {},
    });
  }

  return { FakeHTMLDialogElement, input, pointerDown, click };
}

// 1. Fallback mode (Safari / browsers without closedBy)
{
  const { FakeHTMLDialogElement, input, pointerDown, click } = makeEnv({ withClosedBy: false });
  const dlg = new FakeHTMLDialogElement();
  dlg.showModal();
  assert.strictEqual(dlg.open, true);

  // Drag out from input to backdrop: must NOT close.
  pointerDown(input, 200, 200);
  click(dlg, 20, 20);
  assert.strictEqual(dlg.open, true, 'drag-out from input must not close dialog');
  assert.strictEqual(dlg.closeCalls, 0);

  // Drag out from dialog padding to backdrop: must NOT close.
  pointerDown(dlg, 120, 120);
  click(dlg, 20, 20);
  assert.strictEqual(dlg.open, true, 'drag-out from dialog padding must not close dialog');
  assert.strictEqual(dlg.closeCalls, 0);

  // Drag in from backdrop into dialog: must NOT close.
  pointerDown(dlg, 20, 20);
  click(dlg, 200, 200);
  assert.strictEqual(dlg.open, true, 'drag-in from backdrop must not close dialog');
  assert.strictEqual(dlg.closeCalls, 0);

  // Right-click on backdrop: must NOT close.
  pointerDown(dlg, 20, 20, 2);
  click(dlg, 20, 20);
  assert.strictEqual(dlg.open, true, 'right-click on backdrop must not close dialog');
  assert.strictEqual(dlg.closeCalls, 0);

  // Cancelable dialog (e.g. active ISO upload): must NOT close.
  dlg.cancelDefaultPrevented = true;
  pointerDown(dlg, 20, 20, 0);
  click(dlg, 20, 20);
  assert.strictEqual(dlg.open, true, 'preventDefault on cancel must keep dialog open');
  assert.strictEqual(dlg.closeCalls, 0);
  dlg.cancelDefaultPrevented = false;

  // True backdrop click (down and up outside rect): MUST close.
  pointerDown(dlg, 20, 20, 0);
  click(dlg, 25, 25);
  assert.strictEqual(dlg.open, false, 'backdrop click must close dialog');
  assert.strictEqual(dlg.closeCalls, 1);
}

// 2. Native closedBy mode (Chrome 134+, Firefox 141+)
{
  const { FakeHTMLDialogElement, pointerDown, click } = makeEnv({ withClosedBy: true });
  const dlg = new FakeHTMLDialogElement();
  dlg.showModal();
  assert.strictEqual(dlg.getAttribute('closedby'), 'any', 'showModal must enable closedby="any"');

  // Native closedby="any" handles light-dismiss; JS fallback must not double-close.
  pointerDown(dlg, 20, 20, 0);
  click(dlg, 20, 20);
  assert.strictEqual(dlg.closeCalls, 0, 'JS fallback must defer to native closedby="any"');
}

console.log('DIALOG_BACKDROP_OK');
`

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "-e", testScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node dialog backdrop test failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "DIALOG_BACKDROP_OK") {
		t.Fatalf("unexpected node output: %s", string(out))
	}
}

func TestNoDuplicateDialogBackdropHandlers(t *testing.T) {
	files, err := filepath.Glob("*.js")
	if err != nil {
		t.Fatalf("glob js: %v", err)
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\.nodeName\s*===\s*['"]DIALOG['"]`),
		regexp.MustCompile(`\.tagName\s*===\s*['"]DIALOG['"]`),
		regexp.MustCompile(`event\.target\s*===\s*(dialog|dlg|d|timeDialog)\b`),
		regexp.MustCompile(`ev\.target\s*===\s*(dialog|dlg|d|timeDialog)\b`),
		regexp.MustCompile(`e\.target\s*===\s*(dialog|dlg|d|timeDialog)\b`),
	}
	for _, f := range files {
		if f == "async_block.js" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		text := string(raw)
		for _, re := range patterns {
			if match := re.FindString(text); match != "" {
				t.Errorf("%s still contains a local dialog backdrop handler (%q); backdrop dismiss must be handled centrally in async_block.js", f, match)
			}
		}
	}
}
