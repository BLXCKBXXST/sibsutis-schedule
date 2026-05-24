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

  document.addEventListener('DOMContentLoaded', function () {
    tickTimer();
    setInterval(tickTimer, 30000);
    scrollToToday();
  });
})();
