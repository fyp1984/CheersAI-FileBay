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

  const renderWorkbenchNav = () => {
    if (document.getElementById('filebay-workbench-nav')) return true;

    // Target only RAGFlow's global top navigation. Settings pages also contain
    // a sidebar, so querying a generic <header> would alter that layout.
    const navList = document.querySelector('header nav > ul');
    if (!navList) return false;

    const item = document.createElement('li');
    item.id = 'filebay-workbench-nav';
    item.className = 'filebay-workbench-nav';

    const link = document.createElement('a');
    link.className = 'filebay-workbench-link';
    link.href = fileBayUrl();
    link.textContent = '工作台';
    link.setAttribute('aria-label', '返回 CheersAI FileBay 知识库工作台');
    item.append(link);

    // The first item is RAGFlow's home icon; the second is “知识库”.
    navList.insertBefore(item, navList.children[1] || null);
    return true;
  };

  let attempts = 0;
  const waitForWorkbenchNav = () => {
    if (renderWorkbenchNav() || attempts++ >= 30) return;
    window.setTimeout(waitForWorkbenchNav, 250);
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', waitForWorkbenchNav, { once: true });
  } else {
    waitForWorkbenchNav();
  }
})();
