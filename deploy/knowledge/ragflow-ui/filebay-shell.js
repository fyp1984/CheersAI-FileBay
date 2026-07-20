(() => {
  // RAGFlow defaults to dark mode and English. FileBay's internal trial is a
  // Chinese, light workspace. Do this before React mounts so its first render
  // does not flicker to a different language or colour scheme.
  try {
    window.localStorage.setItem('ragflow-ui-theme', 'light');

    // Migrate an existing trial browser once. Afterwards, a user's explicit
    // language choice in RAGFlow remains intact.
    const languageMigration = 'filebay-ragflow-default-language';
    if (window.localStorage.getItem(languageMigration) !== 'zh-Hans') {
      window.localStorage.setItem('lng', 'zh-Hans');
      window.localStorage.setItem(languageMigration, 'zh-Hans');
    }
  } catch {
    // Storage may be disabled by a managed browser. The page remains usable.
  }

  const fileBayUrl = () => {
    const configured = document.documentElement.dataset.filebayUrl;
    if (configured) return configured;
    return `${window.location.origin}/knowledge#knowledge-engine`;
  };

  const findKnowledgeLink = () => {
    // Match RAGFlow's real dataset route rather than relying on the position
    // of a list item. React can re-order items or mount a compact header on
    // different pages, while this route stays part of the upstream contract.
    return Array.from(document.querySelectorAll('header nav > ul a')).find((link) => {
      try {
        return new URL(link.href, window.location.origin).pathname.endsWith('/datasets');
      } catch {
        return false;
      }
    });
  };

  const renderWorkbenchNav = () => {
    const existing = document.getElementById('filebay-workbench-nav');
    if (existing?.isConnected) return true;

    // Target only RAGFlow's global top navigation. Settings pages also contain
    // a sidebar, so querying a generic <header> would alter that layout.
    const knowledgeLink = findKnowledgeLink();
    const navList = knowledgeLink?.closest('ul');
    if (!navList) return false;

    // Reuse the actual “知识库” link class instead of approximating it in
    // adapter CSS. This makes “工作台” identical to every RAGFlow top-level
    // navigation item across desktop breakpoints and future upstream tweaks.

    const item = document.createElement('li');
    item.id = 'filebay-workbench-nav';

    const link = document.createElement('a');
    link.className = knowledgeLink.className;
    link.href = fileBayUrl();
    link.textContent = '工作台';
    link.setAttribute('aria-label', '返回 CheersAI FileBay 知识库工作台');
    item.append(link);

    // Place the FileBay destination directly before RAGFlow's “知识库”, so the
    // two adjacent entries clearly distinguish governance from retrieval.
    navList.insertBefore(item, knowledgeLink.closest('li'));
    return true;
  };

  let renderScheduled = false;
  const scheduleWorkbenchNav = () => {
    if (renderScheduled) return;
    renderScheduled = true;
    window.queueMicrotask(() => {
      renderScheduled = false;
      renderWorkbenchNav();
    });
  };

  const keepWorkbenchNavMounted = () => {
    scheduleWorkbenchNav();

    // RAGFlow owns this header with React and rebuilds it after route and
    // session changes. Keep the deployment-only link in sync instead of
    // letting a one-time DOM insert disappear after navigation.
    const observer = new MutationObserver(scheduleWorkbenchNav);
    observer.observe(document.documentElement, { childList: true, subtree: true });
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', keepWorkbenchNavMounted, { once: true });
  } else {
    keepWorkbenchNavMounted();
  }
})();
