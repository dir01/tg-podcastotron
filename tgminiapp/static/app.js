// Glue between Telegram, htmx and the pages that the server renders.
(function () {
  'use strict';

  const tg = window.Telegram && window.Telegram.WebApp;
  const initData = (tg && tg.initData) || '';
  const inTelegram = initData !== '';

  // region Telegram

  if (inTelegram) {
    document.documentElement.classList.add('in-telegram');
    tg.ready();
    tg.expand();
    call(() => tg.disableVerticalSwipes()); // a scroll up must not close the app
    call(() => tg.setHeaderColor('secondary_bg_color'));
    call(() => tg.setBackgroundColor('secondary_bg_color'));
    const setScheme = () => { document.documentElement.dataset.scheme = tg.colorScheme; };
    setScheme();
    tg.onEvent('themeChanged', setScheme);
  }

  function call(fn) {
    try { fn(); } catch (e) { /* an old Telegram client does not have this method */ }
  }

  function haptic(type) {
    if (inTelegram) call(() => tg.HapticFeedback.notificationOccurred(type));
  }

  // endregion

  // region htmx

  // Back loads the page from the server again, so that the progress is current.
  htmx.config.historyCacheSize = 0;
  // The server sends errors into the toast (HX-Retarget), so error responses are swapped too.
  htmx.config.responseHandling = [
    { code: '204', swap: false },
    { code: '[23]..', swap: true },
    { code: '[45]..', swap: true, error: true },
    { code: '...', swap: false },
  ];

  // Each request sends the initData, so that the server knows the user.
  document.addEventListener('htmx:configRequest', (e) => {
    if (initData) e.detail.headers['X-Telegram-Init-Data'] = initData;
  });
  // The history restore makes its own request, without htmx:configRequest.
  document.addEventListener('htmx:historyCacheMiss', (e) => {
    if (initData) e.detail.xhr.setRequestHeader('X-Telegram-Init-Data', initData);
  });

  // Telegram shows its own confirmation popup.
  document.addEventListener('htmx:confirm', (e) => {
    if (!e.detail.question || !inTelegram) return;
    e.preventDefault();
    tg.showConfirm(e.detail.question, (ok) => { if (ok) e.detail.issueRequest(true); });
  });

  document.addEventListener('htmx:sendError', () => showToast('No connection. Try again.', 'error'));

  // endregion

  // region toast

  let toastTimer;

  function showToast(text, kind) {
    const el = document.getElementById('toast');
    if (!el) return;
    if (text != null) el.textContent = text;
    el.classList.toggle('toast-error', kind === 'error');
    el.classList.add('toast-visible');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => el.classList.remove('toast-visible'), 4000);
    haptic(kind === 'error' ? 'error' : 'success');
  }

  // Only errors go into the toast element.
  document.addEventListener('htmx:afterSwap', (e) => {
    if (e.detail.target && e.detail.target.id === 'toast') showToast(null, 'error');
  });
  // The server sends success messages with "HX-Trigger: {"toast": "..."}".
  document.addEventListener('toast', (e) => showToast(e.detail.value, 'info'));

  // endregion

  // region navigation

  // depth is the number of pages that the app opened after the first page.
  let depth = 0;

  function syncBackButton() {
    if (!inTelegram) return;
    if (location.pathname === '/') tg.BackButton.hide();
    else tg.BackButton.show();
  }

  if (inTelegram) {
    tg.BackButton.onClick(() => {
      if (depth > 0) {
        history.back();
        return;
      }
      // The app opened on this page (for example from a bot button): go home.
      htmx.ajax('GET', '/', { target: '#main', swap: 'innerHTML' }).then(() => {
        history.replaceState({ htmx: true }, '', '/');
        syncBackButton();
      });
    });
  }

  document.addEventListener('htmx:pushedIntoHistory', () => {
    depth++;
    window.scrollTo(0, 0);
    syncBackButton();
  });
  window.addEventListener('popstate', () => {
    depth = Math.max(0, depth - 1);
    setTimeout(syncBackButton, 0);
  });
  document.addEventListener('htmx:historyRestore', syncBackButton);
  // A replaced entry (a filter, or the page after a finished step) is not a new screen.
  document.addEventListener('htmx:replacedInHistory', syncBackButton);
  syncBackButton();

  // endregion

  // region main button

  // A form with data-main-button gets the Telegram main button. The text is in
  // data-main-button. data-main-button-hidden="true" hides the button, and
  // data-main-button-disabled="true" disables it.
  let mainButtonForm = null;

  function syncMainButton() {
    if (!inTelegram) return;
    const mb = tg.MainButton;
    const form = document.querySelector('#main [data-main-button]');
    if (!form || form.dataset.mainButtonHidden === 'true') {
      mainButtonForm = null;
      mb.hide();
      return;
    }
    mainButtonForm = form;
    mb.setParams({
      text: form.dataset.mainButton,
      is_active: form.dataset.mainButtonDisabled !== 'true',
      is_visible: true,
    });
  }

  if (inTelegram) {
    tg.MainButton.onClick(() => {
      if (!mainButtonForm || !document.body.contains(mainButtonForm)) return;
      if (mainButtonForm.requestSubmit) mainButtonForm.requestSubmit();
      else htmx.trigger(mainButtonForm, 'submit');
    });
    document.addEventListener('htmx:beforeRequest', (e) => {
      if (mainButtonForm && e.detail.elt === mainButtonForm) tg.MainButton.showProgress(false);
    });
    document.addEventListener('htmx:afterRequest', (e) => {
      if (mainButtonForm && e.detail.elt === mainButtonForm) tg.MainButton.hideProgress();
    });
  }

  document.addEventListener('htmx:afterSettle', syncMainButton);

  // endregion

  // region copy, share and rename

  async function copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (e) {
      // Some web views have no clipboard API.
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      let ok = false;
      try { ok = document.execCommand('copy'); } catch (e2) { ok = false; }
      ta.remove();
      return ok;
    }
  }

  document.addEventListener('click', async (e) => {
    const edit = e.target.closest('[data-edit-toggle]');
    if (edit) {
      const form = document.getElementById(edit.dataset.editToggle);
      form.hidden = !form.hidden;
      const input = form.querySelector('input[type=text]');
      if (!form.hidden && input) {
        input.focus();
        input.select();
      }
      return;
    }
    const copy = e.target.closest('[data-copy]');
    if (copy) {
      e.preventDefault();
      if (await copyText(copy.dataset.copy)) showToast('Copied', 'info');
      else showToast('Could not copy. Select the text and copy it.', 'error');
      return;
    }
    const share = e.target.closest('[data-share]');
    if (share) {
      e.preventDefault();
      const url = 'https://t.me/share/url?url=' + encodeURIComponent(share.dataset.share) +
        '&text=' + encodeURIComponent(share.dataset.shareText || '');
      if (inTelegram) tg.openTelegramLink(url);
      else window.open(url, '_blank');
    }
  });

  // endregion

  // region new episodes form

  function formatBytes(n) {
    if (n <= 0) return '0 MB';
    if (n < 1 << 30) return Math.max(1, Math.round(n / (1 << 20))) + ' MB';
    return (n / (1 << 30)).toFixed(1) + ' GB';
  }

  // The order of the selected files is the order of the episodes, or of the parts
  // of a joined episode. A file goes to the end of the list when the user selects it.
  // A button that selects many files adds them in the order of the tree.
  function initNewForm(form) {
    const tree = form.querySelector('[data-tree]');
    const order = []; // paths of the selected files, in the order of selection
    const selected = new Set();
    const sizes = new Map();
    const fileBoxes = (scope) => Array.from(scope.querySelectorAll('input[data-file]'));
    if (tree) for (const box of fileBoxes(tree)) sizes.set(box.value, Number(box.dataset.size) || 0);

    function setSelected(boxes, on) {
      for (const box of boxes) {
        box.checked = on;
        if (on && !selected.has(box.value)) {
          selected.add(box.value);
          order.push(box.value);
        } else if (!on && selected.has(box.value)) {
          selected.delete(box.value);
          order.splice(order.indexOf(box.value), 1);
        }
      }
      update();
    }

    // scopeOf returns the element with the files that a button or a folder checkbox changes.
    function scopeOf(el) {
      const folder = el.closest('details.tree-folder');
      return folder ? folder.querySelector(':scope > .tree-children') : tree;
    }

    function extOf(p) {
      const name = p.split('/').pop();
      const dot = name.lastIndexOf('.');
      return dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
    }

    function renderOrder() {
      const details = form.querySelector('[data-order]');
      if (!details) return;
      details.hidden = order.length < 2;
      if (!details.open) return;
      const list = form.querySelector('[data-order-list]');
      const items = document.createDocumentFragment();
      for (const p of order) {
        const li = document.createElement('li');
        li.textContent = p;
        items.appendChild(li);
      }
      list.replaceChildren(items);
    }

    function update() {
      let count = 1;
      if (tree) {
        count = order.length;
        // A folder is checked if all its files are, and indeterminate if some are.
        for (const folder of tree.querySelectorAll('details.tree-folder')) {
          const box = folder.querySelector(':scope > summary > .folder-check');
          const inside = fileBoxes(folder);
          const n = inside.filter((b) => b.checked).length;
          box.checked = inside.length > 0 && n === inside.length;
          box.indeterminate = n > 0 && n < inside.length;
        }
        const inputs = document.createDocumentFragment();
        let size = 0;
        for (const p of order) {
          const input = document.createElement('input');
          input.type = 'hidden';
          input.name = 'file';
          input.value = p;
          inputs.appendChild(input);
          size += sizes.get(p) || 0;
        }
        form.querySelector('[data-file-inputs]').replaceChildren(inputs);
        setText('[data-selected-count]', count);
        setText('[data-selected-size]', formatBytes(size));
        setText('[data-separate-label]', count + ' episodes');
        const mode = form.querySelector('[data-mode-section]');
        if (mode) mode.hidden = count < 2;
        renderOrder();
      }

      const join = form.querySelector('input[name=mode][value=join]');
      const episodes = count >= 2 && join && join.checked ? 1 : count;
      const label = count === 0 ? 'Choose files' : episodes === 1 ? 'Create 1 episode' : 'Create ' + episodes + ' episodes';
      form.dataset.mainButton = label;
      form.dataset.mainButtonDisabled = count === 0 ? 'true' : 'false';
      const submit = form.querySelector('[data-submit-label]');
      if (submit) {
        submit.textContent = label;
        submit.disabled = count === 0;
      }
      syncMainButton();
    }

    function setText(selector, text) {
      const el = form.querySelector(selector);
      if (el) el.textContent = text;
    }

    form.addEventListener('change', (e) => {
      const t = e.target;
      if (t.matches('input[data-file]')) setSelected([t], t.checked);
      else if (t.classList.contains('folder-check')) setSelected(fileBoxes(scopeOf(t)), t.checked);
      else update();
    });

    form.addEventListener('click', (e) => {
      const button = e.target.closest('[data-select]');
      if (!button) return;
      const boxes = fileBoxes(scopeOf(button));
      switch (button.dataset.select) {
        case 'all':
          setSelected(boxes, true);
          break;
        case 'ext':
          setSelected(boxes.filter((b) => extOf(b.value) === button.dataset.ext), true);
          break;
        case 'none':
          setSelected(boxes, false);
          break;
      }
    });

    const details = form.querySelector('[data-order]');
    if (details) details.addEventListener('toggle', renderOrder);

    update();
  }

  // endregion

  // region episode selection

  function initSelection(form) {
    const bar = form.querySelector('[data-selection-bar]');
    const boxes = () => Array.from(form.querySelectorAll('input[name=ids]'));

    function update() {
      const n = boxes().filter((b) => b.checked).length;
      bar.hidden = n === 0;
      bar.querySelector('[data-selection-count]').textContent = n;
      form.dataset.mainButton = n === 1 ? 'Edit 1 episode' : 'Edit ' + n + ' episodes';
      form.dataset.mainButtonHidden = n === 0 ? 'true' : 'false';
      syncMainButton();
    }

    form.addEventListener('change', update);
    form.querySelector('[data-select-all]').addEventListener('click', () => {
      const all = boxes();
      const check = all.some((b) => !b.checked);
      for (const b of all) b.checked = check;
      update();
    });
    update();
  }

  // endregion

  function init(root, selector, fn) {
    const found = root.matches && root.matches(selector) ? [root] : [];
    for (const el of found.concat(Array.from(root.querySelectorAll(selector)))) {
      if (el.dataset.ready) continue;
      el.dataset.ready = 'true';
      fn(el);
    }
  }

  htmx.onLoad((root) => {
    init(root, '[data-new-form]', initNewForm);
    init(root, '[data-selection]', initSelection);
  });
})();
