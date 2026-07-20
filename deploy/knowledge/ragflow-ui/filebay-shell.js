(() => {
  const fileBayUrl = () => {
    const configured = document.documentElement.dataset.filebayUrl;
    if (configured) return configured;
    return `${window.location.protocol}//${window.location.hostname}:13080/knowledge#knowledge-engine`;
  };

  const renderShell = () => {
    if (document.getElementById('filebay-unified-shell')) return true;
    const header = document.querySelector('header');
    if (!header || !header.parentElement) return false;

    const shell = document.createElement('section');
    shell.id = 'filebay-unified-shell';
    shell.setAttribute('aria-label', 'CheersAI FileBay 与 RAGFlow 导航');
    shell.innerHTML = `
      <div class="filebay-shell-brand">
        <span class="filebay-shell-mark">C</span>
        <span><strong>CheersAI FileBay</strong><small>企业知识库</small></span>
      </div>
      <div class="filebay-shell-context"><span class="filebay-shell-dot"></span>RAGFlow 检索配置</div>
      <a class="filebay-shell-link" href="${fileBayUrl()}">返回知识库工作台 <span aria-hidden="true">↗</span></a>
    `;
    header.insertAdjacentElement('afterend', shell);
    document.title = '检索配置 · CheersAI FileBay';
    return true;
  };

  let attempts = 0;
  const waitForHeader = () => {
    if (renderShell() || attempts++ >= 30) return;
    window.setTimeout(waitForHeader, 250);
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', waitForHeader, { once: true });
  } else {
    waitForHeader();
  }
})();
