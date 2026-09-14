(() => {
  const roots = document.querySelectorAll("[data-fileloom-comments]");
  if (!roots.length) return;

  roots.forEach((root) => {
    const mount = root.querySelector(".artalk");
    const data = root.dataset;
    if (!mount || !data.server || !data.site || !data.pageKey || !window.Artalk?.init) {
      root.dataset.commentsError = "configuration";
      return;
    }
    if (root.__fileloomArtalk) return;

    try {
      root.__fileloomArtalk = window.Artalk.init({
        el: mount,
        server: data.server,
        site: data.site,
        pageKey: data.pageKey,
        pageTitle: data.pageTitle || document.title,
        darkMode: false,
        imgUpload: false,
        emoticons: false,
        preview: false,
        reqTimeout: 12,
        listSort: true,
      });
    } catch {
      root.dataset.commentsError = "initialization";
    }
  });
})();
