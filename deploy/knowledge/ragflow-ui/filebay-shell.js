(() => {
  'use strict';

  // Keep the upstream SPA in Chinese/light mode before React performs its
  // first render. This adapter does not modify upstream source or behaviour.
  try {
    window.localStorage.setItem('ragflow-ui-theme', 'light');

    const languageMigration = 'filebay-ragflow-default-language';
    if (window.localStorage.getItem(languageMigration) !== 'zh-Hans') {
      window.localStorage.setItem('lng', 'zh-Hans');
      window.localStorage.setItem(languageMigration, 'zh-Hans');
    }
  } catch {
    // Managed browsers can block storage. RAGFlow remains usable in that case.
  }

  document.documentElement.classList.add('filebay-ragflow-shell');

  const fileBayUrl = () => {
    const configured = document.documentElement.dataset.filebayUrl;
    return configured || `${window.location.origin}/knowledge#knowledge-engine`;
  };

  const assetUrl = (name) => `${window.location.origin}/ragflow/${name}`;

  const findKnowledgeLink = () =>
    Array.from(document.querySelectorAll('header nav > ul a')).find((link) => {
      try {
        return new URL(link.href, window.location.origin).pathname.endsWith('/datasets');
      } catch {
        return false;
      }
    });

  const renderBrand = () => {
    const logo = document.querySelector('header img[alt="RAGFlow logo"]');
    if (!logo) return false;

    logo.src = assetUrl('cheersai-logo.svg');
    logo.alt = 'CheersAI FileBay';

    const logoLink = logo.closest('a');
    if (logoLink) {
      logoLink.href = fileBayUrl();
      logoLink.setAttribute('aria-label', '返回 CheersAI FileBay 知识库工作台');
    }
    return true;
  };

  const renderWorkbenchNav = () => {
    const existing = document.getElementById('filebay-workbench-nav');
    if (existing?.isConnected) return true;

    // Match the upstream dataset route rather than a hard-coded item index;
    // future RAGFlow navigation ordering therefore remains compatible.
    const knowledgeLink = findKnowledgeLink();
    const navList = knowledgeLink?.closest('ul');
    const knowledgeItem = knowledgeLink?.closest('li');
    if (!navList || !knowledgeItem) return false;

    const item = document.createElement('li');
    item.id = 'filebay-workbench-nav';

    const link = document.createElement('a');
    link.className = knowledgeLink.className;
    link.href = fileBayUrl();
    link.textContent = '工作台';
    link.setAttribute('aria-label', '返回 CheersAI FileBay 知识库工作台');
    item.append(link);

    navList.insertBefore(item, knowledgeItem);
    return true;
  };

  let renderScheduled = false;
  const renderShell = () => {
    if (renderScheduled) return;
    renderScheduled = true;
    window.queueMicrotask(() => {
      renderScheduled = false;
      renderBrand();
      renderWorkbenchNav();
    });
  };

  const keepShellMounted = () => {
    renderShell();

    // React rebuilds the header after route and session changes. Reapply the
    // deployment-only chrome without touching RAGFlow's own React tree.
    const observer = new MutationObserver(renderShell);
    observer.observe(document.documentElement, { childList: true, subtree: true });
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', keepShellMounted, { once: true });
  } else {
    keepShellMounted();
  }
})();
