// Минимальные клиентские улучшения. Без зависимостей.
//
// 1. Live-таймер до начала следующей пары.
//    Серверный шаблон рендерит элемент с data-next-iso (ISO 8601 c TZ);
//    скрипт раз в 30 секунд пересчитывает разницу с now() и пишет её в
//    #next-timer. Когда время прошло — пишет «уже началась», но не
//    перезагружает страницу.
// 2. Якорь #today уже расставляет серверный шаблон; браузер сам прокрутит
//    к нему, если он есть в URL. Этот скрипт добавляет hash и для случая,
//    когда пользователь зашёл на /schedule/... без хэша.

(function () {
  'use strict';

  function formatLeft(ms) {
    if (ms <= 0) return 'уже началась';
    var totalMin = Math.floor(ms / 60000);
    var h = Math.floor(totalMin / 60);
    var m = totalMin % 60;
    if (h >= 24) {
      var days = Math.floor(h / 24);
      h = h % 24;
      return days + ' д ' + h + ' ч';
    }
    if (h > 0) return h + ' ч ' + m + ' мин';
    if (m > 0) return m + ' мин';
    var s = Math.floor(ms / 1000);
    return s + ' с';
  }

  function tickTimer() {
    var strip = document.querySelector('.now-strip[data-next-iso]');
    if (!strip) return;
    var target = strip.querySelector('#next-timer');
    if (!target) return;
    var iso = strip.getAttribute('data-next-iso');
    var at = Date.parse(iso);
    if (Number.isNaN(at)) {
      target.textContent = '—';
      return;
    }
    target.textContent = formatLeft(at - Date.now());
  }

  function scrollToToday() {
    // Если пользователь сам пришёл по якорю — не перепрыгиваем.
    if (window.location.hash) return;
    var today = document.querySelector('[data-today]');
    if (!today) return;
    today.scrollIntoView({behavior: 'instant', block: 'start'});
  }

  // --- Автокомплит в поиске на главной --------------------------------
  // Без зависимостей, через нативный <datalist>. На каждом изменении ввода
  // debounce 250 мс, потом fetch к /api/suggest и переотрисовка <option>.
  function wireSearchAutocomplete() {
    var input = document.getElementById('search-q');
    var list = document.getElementById('search-suggest');
    if (!input || !list) return;

    var typeRadios = document.querySelectorAll('input[name="type"]');
    function currentType() {
      for (var i = 0; i < typeRadios.length; i++) {
        if (typeRadios[i].checked) return typeRadios[i].value;
      }
      return 'group';
    }

    var timer = null;
    var lastKey = '';
    var ctrl = null;

    function fetchSuggest() {
      var q = input.value.trim();
      var typ = currentType();
      var key = typ + '|' + q.toLowerCase();
      if (q.length < 2 || key === lastKey) return;
      lastKey = key;

      if (ctrl) ctrl.abort();
      ctrl = new AbortController();
      var url = '/api/suggest?type=' + encodeURIComponent(typ) +
                '&q=' + encodeURIComponent(q);
      fetch(url, {signal: ctrl.signal})
        .then(function (r) { return r.ok ? r.json() : []; })
        .then(function (items) {
          list.innerHTML = '';
          for (var i = 0; i < items.length; i++) {
            var o = document.createElement('option');
            o.value = items[i].text;
            list.appendChild(o);
          }
        })
        .catch(function () { /* abort или сеть — молча */ });
    }

    function schedule() {
      if (timer) clearTimeout(timer);
      timer = setTimeout(fetchSuggest, 250);
    }

    input.addEventListener('input', schedule);
    // Смена типа сбрасывает список и логику кэша.
    typeRadios.forEach(function (r) {
      r.addEventListener('change', function () {
        list.innerHTML = '';
        lastKey = '';
        if (input.value.trim().length >= 2) schedule();
      });
    });
  }

  // Регистрируем service worker для оффлайн-просмотра. Игнорируем
  // ошибки регистрации — это just-nice-to-have, не критичная фича.
  function registerSW() {
    if (!('serviceWorker' in navigator)) return;
    navigator.serviceWorker.register('/sw.js').catch(function () {});
  }

  // Переключатель темы: 3 положения (auto/light/dark), сохраняется в
  // localStorage. data-theme на <html> уже мог быть выставлен inline-
  // скриптом до загрузки CSS; здесь только обновляем aria-pressed и
  // обрабатываем клики.
  function wireThemeSwitch() {
    var bar = document.querySelector('.theme-switch');
    if (!bar) return;
    function current() {
      try {
        var t = localStorage.getItem('theme');
        if (t === 'light' || t === 'dark') return t;
      } catch (e) {}
      return 'auto';
    }
    function apply(t) {
      document.documentElement.setAttribute('data-theme', t);
      try {
        if (t === 'auto') localStorage.removeItem('theme');
        else localStorage.setItem('theme', t);
      } catch (e) {}
      mark(t);
    }
    function mark(t) {
      var btns = bar.querySelectorAll('button[data-theme-set]');
      btns.forEach(function (b) {
        b.setAttribute('aria-pressed', b.dataset.themeSet === t ? 'true' : 'false');
      });
    }
    bar.addEventListener('click', function (e) {
      var btn = e.target.closest('button[data-theme-set]');
      if (!btn) return;
      apply(btn.dataset.themeSet);
    });
    mark(current());
  }

  document.addEventListener('DOMContentLoaded', function () {
    tickTimer();
    setInterval(tickTimer, 30000);
    scrollToToday();
    wireSearchAutocomplete();
    wireThemeSwitch();
    registerSW();
  });
})();
