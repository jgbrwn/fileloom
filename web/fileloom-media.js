(function () {
  "use strict";

  var ID_PATTERN = /^[A-Za-z0-9_-]{11}$/;
  var ORIGIN = "https://www.youtube-nocookie.com";

  function activate(button) {
    var figure = button.closest("[data-fileloom-youtube]");
    var id = figure && figure.getAttribute("data-youtube-id");
    if (!figure || !ID_PATTERN.test(id || "")) return;

    var title = figure.getAttribute("data-youtube-title") || "YouTube video";
    var fallback = figure.querySelector("[data-fileloom-youtube-fallback]");
    var iframe = document.createElement("iframe");
    iframe.title = title;
    iframe.loading = "lazy";
    iframe.referrerPolicy = "strict-origin-when-cross-origin";
    iframe.allow = "accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share";
    iframe.allowFullscreen = true;
    iframe.src = ORIGIN + "/embed/" + encodeURIComponent(id) + "?autoplay=1&rel=0";
    figure.replaceChildren(iframe);
    if (fallback) figure.appendChild(fallback);
    figure.classList.add("fileloom-youtube-activated");
  }

  function start() {
    document.querySelectorAll("[data-fileloom-youtube-load]").forEach(function (button) {
      button.addEventListener("click", function () { activate(button); }, { once: true });
    });
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start, { once: true });
  else start();
}());
