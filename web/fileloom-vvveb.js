(function () {
  "use strict";

  const languages = [
    { value: "plaintext", text: "Plain text" },
    { value: "javascript", text: "JavaScript" },
    { value: "typescript", text: "TypeScript" },
    { value: "html", text: "HTML" },
    { value: "css", text: "CSS" },
    { value: "json", text: "JSON" },
    { value: "go", text: "Go" },
    { value: "python", text: "Python" },
    { value: "bash", text: "Bash" },
    { value: "sql", text: "SQL" }
  ];

  function languageClass(value) {
    return "language-" + String(value || "plaintext").toLowerCase().replace(/[^a-z0-9_-]/g, "-");
  }

  function registerCodeBlock() {
    if (!window.Vvveb || !Vvveb.Components || Vvveb.Components.get("html/fileloom-code-block")) return;
    if (Vvveb.ComponentsGroup && Array.isArray(Vvveb.ComponentsGroup.Base) && !Vvveb.ComponentsGroup.Base.includes("html/fileloom-code-block")) {
      Vvveb.ComponentsGroup.Base.push("html/fileloom-code-block");
    }
    Vvveb.Components.add("html/fileloom-code-block", {
      nodes: ["pre.fileloom-code-block", "pre[data-fileloom-code]"],
      name: "Code block",
      image: "icons/code.svg",
      html: '<pre class="fileloom-code-block" data-fileloom-code data-language="plaintext"><code class="language-plaintext" data-language="plaintext">Paste code here</code></pre>',
      properties: [{
        name: "Language",
        key: "data-language",
        htmlAttr: "data-language",
        inputtype: SelectInput,
        data: { options: languages },
        onChange: function (node, value) {
          const code = node.querySelector("code");
          if (code) {
            code.setAttribute("data-language", value || "plaintext");
            code.className = languageClass(value);
          }
          return value || "plaintext";
        }
      }, {
        name: "Code",
        key: "text",
        child: "code",
        htmlAttr: "innerText",
        inline: false,
        inputtype: TextareaInput,
        data: { rows: 18 }
      }]
    });
  }

  function uploadFiles(modal, files) {
    files = Array.from(files || []).filter(Boolean);
    if (!files.length) return;
    const status = modal.container && modal.container.querySelector(".upload-collapse .status");
    modal.showUploadLoading();
    (async function () {
      for (let index = 0; index < files.length; index += 1) {
        const file = files[index];
        if (status) status.textContent = `Uploading ${index + 1} of ${files.length}: ${file.name}`;
        const form = new FormData();
        form.append("file", file, file.name);
        form.append("mediaPath", (window.mediaPath || "/media") + (modal.currentPath || ""));
        const response = await fetch(window.uploadUrl || "/_cms/api/media?format=vvveb", { method: "POST", body: form });
        const text = await response.text();
        if (!response.ok) throw new Error(text || response.statusText);
        let name = text.trim();
        try {
          const data = JSON.parse(name);
          name = data.name || data.path || name;
        } catch (_) {}
        name = name.replace(/^.*[\\/]/, "");
        if (!name) throw new Error("Upload response did not include a filename");
        const currentPath = String(modal.currentPath || "").replace(/^\/+/, "");
        const fileElement = modal.addFile({ name, type: "file", path: "/" + (currentPath ? currentPath + "/" : "") + name, size: file.size }, true);
        fileElement && fileElement.scrollIntoView({ behavior: "smooth", block: "center", inline: "center" });
      }
      modal.hideUploadLoading();
      if (status) status.textContent = `${files.length} file${files.length === 1 ? "" : "s"} uploaded.`;
    })().catch(function (error) {
      modal.hideUploadLoading();
      if (status) status.textContent = error.message || "Upload failed";
      if (typeof displayToast === "function") displayToast("bg-danger", "Upload error", (error.message || "Upload failed").slice(0, 200));
    });
  }

  function wireMediaModal() {
    if (typeof MediaModal === "undefined" || !MediaModal.prototype || MediaModal.prototype.__fileloomPatched) return;
    const originalInit = MediaModal.prototype.init;
    MediaModal.prototype.__fileloomPatched = true;
    MediaModal.prototype.init = function () {
      originalInit.apply(this, arguments);
      const modal = this;
      const container = modal.container;
      if (!container || container.dataset.fileloomMediaReady) return;
      container.dataset.fileloomMediaReady = "1";
      const input = container.querySelector('.filemanager input[type="file"]');
      const zone = container.querySelector(".upload-collapse");
      if (!input || !zone) return;
      input.accept = ".avif,.gif,.jpeg,.jpg,.png,.svg,.webp,.mp3,.mp4,.pdf,.webm";
      input.multiple = true;
      input.removeEventListener("change", modal.onUpload);
      input.addEventListener("change", function () {
        uploadFiles(modal, this.files);
        this.value = "";
      });
      zone.classList.add("fileloom-upload-dropzone");
      zone.addEventListener("dragover", function (event) {
        event.preventDefault();
        zone.classList.add("fileloom-drag-over");
      });
      zone.addEventListener("dragleave", function () { zone.classList.remove("fileloom-drag-over"); });
      zone.addEventListener("drop", function (event) {
        event.preventDefault();
        zone.classList.remove("fileloom-drag-over");
        uploadFiles(modal, event.dataTransfer && event.dataTransfer.files);
      });
    };
  }

  registerCodeBlock();
  wireMediaModal();
  window.FileloomVvveb = { registerCodeBlock, wireMediaModal };
}());
