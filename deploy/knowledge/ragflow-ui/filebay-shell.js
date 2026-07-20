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

  const renderReturnLink = () => {
    if (document.getElementById('filebay-return-link')) return;

    // Do not insert into RAGFlow's component tree. Its settings pages use a
    // sidebar <header>, while the main workspace uses a global <header>.
    // Injecting into either one changes the flex/grid layout and caused the
    // blank left column shown in the previous build.
    const link = document.createElement('a');
    link.id = 'filebay-return-link';
    link.className = 'filebay-return-link';
    link.href = fileBayUrl();
    link.textContent = '返回知识库工作台';
    link.setAttribute('aria-label', '返回 CheersAI FileBay 知识库工作台');
    document.body.append(link);
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', renderReturnLink, { once: true });
  } else {
    renderReturnLink();
  }
})();
