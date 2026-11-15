/* ── Parham FX: click sound effects + sparks ─────────────────────────
   Ported from parham's own panel theme (parham-fx) to vanilla JS.
   Sounds: Kenney "Interface Sounds" (CC0), served from /static/sfx/.
   Scope: tap blip + .ogg bank (toggle/open/close/success/error/notify/
   click) and the 9-spark burst on buttons. Nothing else. */
(function () {
  'use strict';
  try {
    var SFX_BASE = '/static/sfx/';

    function playSfx(name, vol) {
      try {
        var a = new Audio(SFX_BASE + name + '.ogg');
        a.volume = vol || 0.4;
        var p = a.play();
        if (p && typeof p.catch === 'function') p.catch(function () {});
      } catch (e) { /* ignore */ }
    }

    /* Subtle tactile blip on real taps — never on scroll touches. */
    var audioCtx = null;
    function blip(soft) {
      try {
        if (!audioCtx) audioCtx = new (window.AudioContext || window.webkitAudioContext)();
        if (audioCtx.state === 'suspended') audioCtx.resume();
        var t = audioCtx.currentTime + 0.01;
        var o = audioCtx.createOscillator();
        var g = audioCtx.createGain();
        o.type = 'square';
        o.frequency.setValueAtTime(soft ? 520 : 760, t);
        o.frequency.exponentialRampToValueAtTime(soft ? 390 : 1180, t + 0.055);
        g.gain.setValueAtTime(soft ? 0.028 : 0.045, t);
        g.gain.exponentialRampToValueAtTime(0.0001, t + 0.075);
        o.connect(g);
        g.connect(audioCtx.destination);
        o.start(t);
        o.stop(t + 0.08);
      } catch (e) { /* ignore */ }
    }

    function reducedMotion() {
      return typeof window.matchMedia === 'function' &&
        window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    }

    function spawnSparks(el, x, y) {
      for (var i = 0; i < 9; i++) {
        var a = (Math.PI * 2 * i) / 9 + Math.random() * 0.5;
        var d = 28 + Math.random() * 42;
        var spark = document.createElement('span');
        spark.className = 'px-spark';
        spark.style.left = x + 'px';
        spark.style.top = y + 'px';
        spark.style.setProperty('--dx', (Math.cos(a) * d) + 'px');
        spark.style.setProperty('--dy', (Math.sin(a) * d) + 'px');
        el.appendChild(spark);
        (function (sp) {
          sp.addEventListener('animationend', function () { sp.remove(); }, { once: true });
          setTimeout(function () { if (sp.parentNode) sp.remove(); }, 900);
        })(spark);
      }
    }

    function ensureSparkScope(el) {
      if (el instanceof HTMLElement && !el.classList.contains('px-needs-spark')) {
        el.classList.add('px-needs-spark');
      }
    }

    function closestInteractive(target) {
      if (!(target instanceof Element) || typeof target.closest !== 'function') return null;
      return target.closest('button, a, [role="button"], [role="tab"], [role="menuitem"], [role="option"], summary, input[type="checkbox"], input[type="radio"], select');
    }

    /* ── tap-verified feedback (silent on scroll) ── */
    var taps = new Map();
    document.addEventListener('pointerdown', function (e) {
      if (e.isPrimary === false) return;
      var el = closestInteractive(e.target);
      if (!el) return;
      taps.set(e.pointerId, { x: e.clientX, y: e.clientY, t: Date.now(), el: el, moved: false });
      if (el instanceof HTMLElement && (el.tagName === 'BUTTON' || el.classList.contains('button'))) {
        ensureSparkScope(el);
      }
    }, true);
    document.addEventListener('pointermove', function (e) {
      var r = taps.get(e.pointerId);
      if (!r) return;
      if (Math.abs(e.clientX - r.x) + Math.abs(e.clientY - r.y) > 12) r.moved = true;
    }, true);
    document.addEventListener('pointerup', function (e) {
      var r = taps.get(e.pointerId);
      if (!r) return;
      taps.delete(e.pointerId);
      if (r.moved || Date.now() - r.t > 600) return;
      var cb = e.target instanceof Element ? e.target.closest('input[type="checkbox"],input[type="radio"]') : null;
      var sw = e.target instanceof Element ? e.target.closest('[role="switch"]') : null;
      if (cb || sw) { playSfx('toggle', 0.3); return; }
      var btn = r.el.closest('button, .button');
      if (btn) {
        blip(false);
        try { if (navigator.vibrate) navigator.vibrate(8); } catch (err) { /* ignore */ }
        if (!reducedMotion()) {
          var rc = btn.getBoundingClientRect();
          var cx = (e.clientX || rc.left + rc.width / 2) - rc.left;
          var cy = (e.clientY || rc.top + rc.height / 2) - rc.top;
          ensureSparkScope(btn);
          spawnSparks(btn, cx, cy);
        }
      } else {
        playSfx('click', 0.35);
      }
    }, true);
    document.addEventListener('pointercancel', function (e) { taps.delete(e.pointerId); }, true);

    /* ── select/checkbox change → toggle sound ── */
    document.addEventListener('change', function (e) {
      var t = e.target;
      if (t instanceof Element && t.matches('input[type="checkbox"],input[type="radio"],select')) {
        playSfx('toggle', 0.3);
      }
    }, true);

    /* ── modals (open/close) + toast (success/error/notify) ──
       WhitePanel toggles a .show class on .modal elements and on #toast;
       the toast's inline color carries its type (var(--red) / var(--green)). */
    var shownModals = new WeakSet();
    var toastShown = false;
    var obs = new MutationObserver(function (muts) {
      for (var i = 0; i < muts.length; i++) {
        var el = muts[i].target;
        if (!(el instanceof Element)) continue;
        if (el.classList && el.classList.contains('modal')) {
          var has = el.classList.contains('show');
          if (has && !shownModals.has(el)) { shownModals.add(el); playSfx('open', 0.35); }
          if (!has && shownModals.has(el)) { shownModals.delete(el); playSfx('close', 0.4); }
        }
        if (el.id === 'toast' || (el.classList && el.classList.contains('toast'))) {
          var tHas = el.classList.contains('show');
          if (tHas && !toastShown) {
            toastShown = true;
            var c = (el.getAttribute('style') || '') + '|' + (el.className || '');
            if (c.indexOf('--red') !== -1 || c.indexOf('err') !== -1) playSfx('error', 0.4);
            else if (c.indexOf('--green') !== -1 || c.indexOf('ok') !== -1) playSfx('success', 0.4);
            else playSfx('notify', 0.35);
          }
          if (!tHas) toastShown = false;
        }
      }
    });
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'], subtree: true });
  } catch (e) { /* FX must never break the panel */ }
})();
