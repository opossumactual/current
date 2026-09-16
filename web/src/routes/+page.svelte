<script lang="ts">
  import { onMount, tick } from 'svelte';
  import FeedManager from '$lib/FeedManager.svelte';
  import {
    Radio,
    Search,
    Bookmark,
    Inbox,
    ChevronRight,
    ChevronLeft,
    ArrowUpRight,
    Sun,
    Moon,
    PanelLeftClose,
    PanelLeftOpen,
    Rows3,
    LayoutGrid,
    Check,
    RefreshCw,
    Maximize2,
    Minimize2,
    Type,
    Keyboard,
    ArrowDown,
    ArrowUp,
    X,
    Undo2,
    Menu,
    Signal,
    AlertCircle,
    BookOpen,
    Folder,
    SlidersHorizontal,
    Plus
  } from 'lucide-svelte';
  type Article = {
    id: number;
    feedId: number;
    feedTitle: string;
    title: string;
    summary: string;
    content?: string;
    fulltext?: string;
    imageUrl: string;
    url: string;
    author: string;
    publishedAt: string;
    read: boolean;
    starred: boolean;
  };
  type Feed = {
    id: number;
    title: string;
    categoryId: number | null;
    url: string;
    unread: number;
    lastError: string;
    lastFetchedAt: string;
  };
  type Category = { id: number; title: string };
  let feeds: Feed[] = $state([]),
    categories: Category[] = $state([]),
    items: Article[] = $state([]),
    article: Article | null = $state(null);
  let counts = $state({
    total: 0,
    starred: 0,
    categories: {} as Record<string, number>,
    feeds: {} as Record<string, number>
  });
  let library = $state({ feeds: 0, articles: 0, categories: 0 });
  let manage = $state(''),
    newStories = $state(0),
    connected = $state(true);
  let category: number | null = $state(null),
    feed: number | null = $state(null),
    status = $state('all'),
    query = $state(''),
    next = $state('');
  let expanded: number | null = $state(null),
    compact = $state(false),
    dark = $state(false),
    focus = $state(false),
    fontSize = $state(19),
    showHelp = $state(false),
    showHealth = $state(false),
    mobileNav = $state(false),
    mobileRead = $state(false);
  let busy = $state(true),
    opening = $state(false),
    error = $state(''),
    toast = $state(''),
    undoStack: { id: number; read: boolean; starred: boolean }[][] = $state([]);
  let reader: HTMLElement, searchBox: HTMLInputElement, scrollBox: HTMLElement;
  let failedImages: Set<number> = $state(new Set()),
    listGeneration = 0,
    articleGeneration = 0,
    debounce: ReturnType<typeof setTimeout>,
    toastTimer: ReturnType<typeof setTimeout>;
  let positions = new Map<number, number>();
  let pending = new Set<number>();
  const title = $derived(
    feed
      ? feeds.find((f) => f.id === feed)?.title || 'Feed'
      : category
        ? categories.find((c) => c.id === category)?.title || 'Collection'
        : status === 'starred'
          ? 'Saved for later'
          : status === 'unread'
            ? 'Unread articles'
            : 'All articles'
  );
  const subtitle = $derived(
    feed
      ? 'A closer look at one source.'
      : category
        ? 'A little more context. A clearer picture.'
        : status === 'starred'
          ? 'Good things to come back to.'
          : 'Your sources. Your perspective.'
  );
  const currentIndex = $derived(items.findIndex((x) => x.id === article?.id));
  function words(a: Article | null) {
    return (a?.fulltext || a?.content || a?.summary || '')
      .replace(/<[^>]*>/g, ' ')
      .split(/\s+/).length;
  }
  const wordCount = $derived(words(article));
  const issueFeeds = $derived(feeds.filter((f) => f.lastError));
  async function api(path: string, options: RequestInit = {}) {
    const r = await fetch('/api/' + path, {
      ...options,
      headers: { 'Content-Type': 'application/json', ...options.headers }
    });
    if (!r.ok)
      throw new Error(
        (await r.json().catch(() => ({ error: r.statusText }))).error
      );
    return r.status === 204 ? null : r.json();
  }
  function notice(t: string) {
    toast = t;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (toast = ''), 4500);
  }
  function date(v: string) {
    return new Date(v).toLocaleDateString(undefined, {
      month: 'short',
      day: 'numeric'
    });
  }
  function clock(v: string) {
    return new Date(v).toLocaleTimeString(undefined, {
      hour: '2-digit',
      minute: '2-digit'
    });
  }
  function plain(v: string) {
    return v
      .replace(/<[^>]*>/g, ' ')
      .replace(/&amp;/g, '&')
      .replace(/&nbsp;/g, ' ')
      .replace(/&#39;/g, "'")
      .replace(/&quot;/g, '"');
  }
  function imageFailed(id: number) {
    failedImages = new Set([...failedImages, id]);
  }
  function parameters() {
    const p = new URLSearchParams({ status, limit: '100' });
    if (category) p.set('category', String(category));
    if (feed) p.set('feed', String(feed));
    if (query.trim()) p.set('q', query.trim());
    return p;
  }
  async function load(keep = false, more = false) {
    const gen = ++listGeneration;
    busy = true;
    error = '';
    try {
      const p = parameters();
      if (more && next) p.set('cursor', next);
      const data = await api('articles?' + p);
      if (gen !== listGeneration) return;
      items = more ? [...items, ...data.items] : data.items;
      next = data.next;
      counts = await api('counts');
      if (!keep || !items.some((x) => x.id === article?.id)) {
        if (items.length) await open(items[0], false);
        else {
          article = null;
          articleGeneration++;
        }
      }
    } catch (e) {
      if (gen === listGeneration) error = String(e);
    } finally {
      if (gen === listGeneration) busy = false;
    }
  }
  async function open(a: Article, mobile = true) {
    if (reader && article) positions.set(article.id, reader.scrollTop);
    const gen = ++articleGeneration;
    article = a;
    opening = true;
    mobileRead = mobile;
    try {
      const full = await api('articles/' + a.id);
      if (gen !== articleGeneration) return;
      article = full;
      await tick();
      if (reader) reader.scrollTop = positions.get(a.id) || 0;
    } catch (e) {
      if (gen === articleGeneration) error = String(e);
    } finally {
      if (gen === articleGeneration) opening = false;
    }
  }
  function select(c: number | null = null, f: number | null = null, s = 'all') {
    category = c;
    feed = f;
    status = s;
    mobileNav = false;
    mobileRead = false;
    load();
  }
  function search() {
    clearTimeout(debounce);
    debounce = setTimeout(() => load(), 220);
  }
  async function patch(a: Article, change: Partial<Article>, record = true) {
    if (pending.has(a.id)) return false;
    pending.add(a.id);
    try {
      const old = { id: a.id, read: a.read, starred: a.starred };
      const value = await api('articles/' + a.id, {
        method: 'PATCH',
        body: JSON.stringify(change)
      });
      items = items.map((x) => (x.id === a.id ? { ...x, ...value } : x));
      if (article?.id === a.id)
        article = {
          ...article,
          ...value,
          content: article.content,
          fulltext: article.fulltext
        };
      if (record) undoStack = [...undoStack, [old]].slice(-30);
      counts = await api('counts');
      notice(
        change.starred !== undefined
          ? change.starred
            ? 'Saved for later'
            : 'Removed from saved'
          : change.read
            ? 'Marked as read'
            : 'Marked as unread'
      );
      return true;
    } catch (e) {
      error = String(e);
      return false;
    } finally {
      pending.delete(a.id);
    }
  }
  async function undo() {
    const last = undoStack.at(-1);
    if (!last) return;
    try {
      for (const old of last) {
        if (
          !(await patch(
            { ...old } as Article,
            { read: old.read, starred: old.starred },
            false
          ))
        )
          return;
      }
      undoStack = undoStack.slice(0, -1);
      notice('Last action undone');
    } catch (e) {
      error = String(e);
    }
  }
  async function step(d: number) {
    const idx = Math.max(0, Math.min(items.length - 1, currentIndex + d));
    if (items[idx]) await open(items[idx], mobileRead);
  }
  function preference() {
    localStorage.setItem(
      'current.preferences',
      JSON.stringify({ dark, compact, fontSize })
    );
  }
  function toggleTheme() {
    dark = !dark;
    preference();
  }
  function dialogFocus(node: HTMLElement) {
    const previous = document.activeElement as HTMLElement;
    node.querySelector<HTMLButtonElement>('button')?.focus();
    function trap(e: KeyboardEvent) {
      if (e.key !== 'Tab') return;
      const all = Array.from(
        node.querySelectorAll<HTMLElement>('button,a,input')
      );
      const first = all[0],
        last = all.at(-1);
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last?.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first?.focus();
      }
    }
    node.addEventListener('keydown', trap);
    return {
      destroy() {
        node.removeEventListener('keydown', trap);
        previous?.focus();
      }
    };
  }
  function key(e: KeyboardEvent) {
    if (manage) return;
    if ((showHelp || showHealth) && e.key !== 'Escape') return;
    if (
      e.target instanceof HTMLElement &&
      (e.target.matches('input,textarea,select') || e.target.isContentEditable)
    )
      return;
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    const k = e.key;
    if (['j', 'k', '/', 's', 'm', 'u', 'z', '?', 'Escape'].includes(k))
      e.preventDefault();
    if (k === 'j') step(1);
    if (k === 'k') step(-1);
    if (k === '/') searchBox?.focus();
    if (k === 's' && article) patch(article, { starred: !article.starred });
    if (k === 'm' && article) patch(article, { read: !article.read });
    if (k === 'u') undo();
    if (k === 'z') focus = !focus;
    if (k === '?') showHelp = !showHelp;
    if (k === 'Escape') {
      showHelp = false;
      showHealth = false;
      focus = false;
      mobileNav = false;
      mobileRead = false;
    }
    if (k === 'g') manage = 'feeds';
    if (k === 'a') manage = 'add';
    if (k === 'r') {
      newStories = 0;
      load(true);
    }
    if (k === 'o' && article)
      window.open(article.url, '_blank', 'noopener,noreferrer');
  }
  async function syncLibrary() {
    const previous = library.articles;
    [feeds, categories, library, counts] = await Promise.all([
      api('feeds'),
      api('categories'),
      api('library'),
      api('counts')
    ]);
    connected = true;
    if (previous && library.articles > previous)
      newStories += library.articles - previous;
    if (
      (feed && !feeds.some((f) => f.id === feed)) ||
      (category && !categories.some((c) => c.id === category))
    ) {
      category = null;
      feed = null;
      await load(true);
    }
  }
  async function refreshFeeds() {
    try {
      await api('refresh', { method: 'POST' });
      notice(
        'Feed updates queued. New stories will appear as sources respond.'
      );
    } catch (e) {
      error = String(e);
    }
  }
  async function managerChanged() {
    await syncLibrary();
    await load(true);
    newStories = 0;
  }
  async function fulltext() {
    if (!article || opening) return;
    const id = article.id;
    opening = true;
    try {
      const full = await api('articles/' + id + '/fulltext', {
        method: 'POST'
      });
      if (article?.id === id) article = full;
    } catch (e) {
      error = String(e);
    } finally {
      opening = false;
    }
  }
  onMount(() => {
    try {
      const p = JSON.parse(localStorage.getItem('current.preferences') || '{}');
      dark = p.dark ?? false;
      compact = p.compact ?? false;
      fontSize = p.fontSize ?? 19;
    } catch {}
    (async () => {
      try {
        await syncLibrary();
        await load();
      } catch (e) {
        error = String(e);
        busy = false;
        connected = false;
      }
    })();
    let syncing = false;
    const timer = setInterval(async () => {
      if (syncing) return;
      syncing = true;
      try {
        await syncLibrary();
        if (article && !opening && !pending.has(article.id)) {
          const id = article.id;
          const fresh = await api('articles/' + id);
          if (article?.id === id && !pending.has(id)) {
            article = { ...article, read: fresh.read, starred: fresh.starred };
            items = items.map((x) =>
              x.id === id
                ? { ...x, read: fresh.read, starred: fresh.starred }
                : x
            );
          }
        }
      } catch {
        connected = false;
      } finally {
        syncing = false;
      }
    }, 4000);
    return () => {
      clearInterval(timer);
      clearTimeout(debounce);
      clearTimeout(toastTimer);
    };
  });
</script>

<svelte:head
  ><title>Current — A clearer view</title><meta
    name="description"
    content="A personal reading space for your feeds."
  /></svelte:head
>
<svelte:window onkeydown={key} />
<div
  class:dark
  class:focus
  class:compact
  class:mobile-read={mobileRead}
  class="app"
>
  <aside class:mobile-open={mobileNav} aria-label="Feed navigation">
    <a class="brand" href="/" aria-label="Current home"
      ><span class="brandmark"><Radio size={21} /></span>current<span
        class="edition">01</span
      ></a
    >
    <div class="workspace">
      <span class="avatar">C</span>
      <div>Your reading room<small>Personal library</small></div>
      <span class="live-dot"></span>
    </div>
    <button class="add-source" onclick={() => (manage = 'add')}
      ><Plus size={16} />Add feed<kbd>A</kbd></button
    >
    <p class="nav-label">YOUR LIBRARY</p>
    <nav class="primary-nav">
      <button
        class:active={!category && !feed && status === 'all'}
        onclick={() => select()}
        ><Inbox size={17} />All articles<span
          >{library.articles.toLocaleString()}</span
        ></button
      >
      <button
        class:active={!category && !feed && status === 'unread'}
        onclick={() => select(null, null, 'unread')}
        ><BookOpen size={17} />Unread<span>{counts.total.toLocaleString()}</span
        ></button
      >
      <button
        class:active={!category && !feed && status === 'starred'}
        onclick={() => select(null, null, 'starred')}
        ><Bookmark size={17} />Saved for later<span
          >{counts.starred || '—'}</span
        ></button
      >
    </nav>
    <div class="nav-label folder-heading">
      <span>COLLECTIONS</span><span>{categories.length}</span>
    </div>
    <nav class="collections" aria-label="Collections">
      {#each categories as c}
        <div class="collection-row" class:active={category === c.id && !feed}>
          <button
            class="collection-name"
            onclick={() => select(c.id)}
            title={c.title}
            ><span class="folder-icon"><Folder size={15} /></span><span
              class="truncate">{c.title}</span
            ><span class="count">{counts.categories[c.id] || 0}</span></button
          >
          <button
            class="expand"
            aria-label={'Show feeds in ' + c.title}
            aria-expanded={expanded === c.id}
            onclick={() => (expanded = expanded === c.id ? null : c.id)}
            ><ChevronRight
              size={13}
              class={expanded === c.id ? 'rotated' : ''}
            /></button
          >
        </div>
        {#if expanded === c.id}<div class="feed-tree">
            {#each feeds.filter((f) => f.categoryId === c.id) as f}<button
                class:active={feed === f.id}
                onclick={() => select(c.id, f.id)}
                title={f.title}
                ><span class="tiny-dot" class:warning={!!f.lastError}
                ></span><span class="truncate">{f.title}</span><small
                  >{counts.feeds[f.id] || 0}</small
                ></button
              >{/each}
          </div>{/if}
      {/each}
      {#if feeds.some((f) => !f.categoryId)}<p
          class="nav-label"
          style="margin-top:18px"
        >
          UNCATEGORIZED
        </p>
        <div class="feed-tree">
          {#each feeds.filter((f) => !f.categoryId) as f}<button
              class:active={feed === f.id}
              onclick={() => select(null, f.id)}
              ><span class="tiny-dot"></span><span class="truncate"
                >{f.title || f.url}</span
              ><small>{counts.feeds[f.id] || 0}</small></button
            >{/each}
        </div>{/if}
    </nav>
    <div class="sidebar-bottom">
      <button onclick={() => (manage = 'feeds')}
        ><SlidersHorizontal size={15} />Manage feeds<span
          >{library.feeds} sources <ArrowUpRight size={12} /></span
        ></button
      >
      <div>
        <span class="live-dot"></span>{connected
          ? 'Live updates'
          : 'Reconnecting…'}
        <button
          class="shortcut-help"
          onclick={() => (showHelp = true)}
          aria-label="Keyboard shortcuts"
          ><Keyboard size={15} /><kbd>?</kbd></button
        >
      </div>
    </div>
  </aside>
  <main>
    <header class="topbar">
      <button
        class="icon mobile-menu"
        aria-label="Open navigation"
        onclick={() => (mobileNav = !mobileNav)}><Menu size={19} /></button
      >
      <div class="breadcrumb">
        Reading room <ChevronRight size={13} /><span
          >{category ? 'Collections' : 'Library'}</span
        >
      </div>
      <div class="topbar-right">
        <span class="prototype-label">YOUR DAILY CURRENT</span><span
          class="separator"
        ></span><button
          class="icon"
          aria-label={dark ? 'Use light theme' : 'Use dark theme'}
          onclick={toggleTheme}
          >{#if dark}<Sun size={17} />{:else}<Moon size={17} />{/if}</button
        ><button
          class="icon"
          aria-label="Keyboard shortcuts"
          onclick={() => (showHelp = true)}><Keyboard size={18} /></button
        >
      </div>
    </header>
    <section class="collection-header">
      <div>
        <p class="eyebrow">A CLEARER VIEW</p>
        <h1>{title}</h1>
        <p class="subtitle">{subtitle}</p>
      </div>
      <div class="collection-meta">
        <strong
          >{(category
            ? counts.categories[category] || 0
            : counts.total
          ).toLocaleString()}</strong
        ><span>unread articles</span>
      </div>
    </section>
    <div class="toolbar">
      <div class="filters" aria-label="Article filter">
        {#each [['all', 'All articles'], ['unread', 'Unread'], ['starred', 'Saved']] as [v, label]}<button
            class:selected={status === v}
            onclick={() => {
              status = v;
              load();
            }}
            >{label}{#if v === 'unread'}<span class="filter-dot"
              ></span>{/if}</button
          >{/each}
      </div>
      <div class="tools">
        <label class="search"
          ><Search size={15} /><input
            bind:this={searchBox}
            bind:value={query}
            oninput={search}
            placeholder="Search this collection"
            aria-label="Search articles"
          />{#if query}<button
              aria-label="Clear search"
              onclick={() => {
                query = '';
                load();
              }}><X size={13} /></button
            >{:else}<kbd>/</kbd>{/if}</label
        ><button
          class="icon"
          aria-label={compact ? 'Use comfortable list' : 'Use compact list'}
          onclick={() => {
            compact = !compact;
            preference();
          }}><Rows3 size={17} /></button
        ><button
          class="icon"
          aria-label="Update all feeds"
          onclick={refreshFeeds}
          ><RefreshCw size={16} class={busy ? 'spinning' : ''} /></button
        >
      </div>
    </div>
    {#if error}<div class="error" role="alert">
        <AlertCircle size={16} />{error}<button onclick={() => load(true)}
          >Retry</button
        ><button aria-label="Dismiss error" onclick={() => (error = '')}
          ><X size={14} /></button
        >
      </div>{/if}
    {#if newStories}<button
        class="new-stories"
        onclick={() => {
          newStories = 0;
          load(true);
        }}
        >{newStories} new {newStories === 1 ? 'story' : 'stories'} in your library
        · Show latest <ArrowUp size={13} /></button
      >{/if}
    <div class="reading-layout">
      <section class="article-list" aria-label="Articles" bind:this={scrollBox}>
        <div class="list-heading">
          <span>{query ? 'SEARCH RESULTS' : 'LATEST ARTICLES'}</span><span
            >Newest first <ArrowDown size={11} /></span
          >
        </div>
        {#if busy && !items.length}<div class="empty">
            <Radio size={26} />
            <h3>Gathering your stories…</h3>
          </div>{/if}
        {#each items as a}
          <button
            class="article-row"
            class:chosen={article?.id === a.id}
            class:read={a.read}
            onclick={() => open(a)}
            aria-current={article?.id === a.id ? 'true' : undefined}
          >
            <div class="row-top">
              <span class="source-badge"
                >{a.feedTitle.slice(0, 1).toUpperCase()}</span
              ><span class="source truncate">{a.feedTitle}</span><time
                >{date(a.publishedAt)}</time
              >{#if a.starred}<Bookmark size={12} fill="currentColor" />{/if}
            </div>
            <div class="row-body">
              <div>
                <h2>{a.title}</h2>
                {#if !compact}<p>{plain(a.summary)}</p>{/if}
              </div>
              {#if !compact && a.imageUrl && !failedImages.has(a.id)}<img
                  class="thumb"
                  src={'/api/image/' + a.id}
                  alt=""
                  loading="lazy"
                  onerror={() => imageFailed(a.id)}
                />{/if}
            </div>
            <div class="row-bottom">
              <span>{clock(a.publishedAt)}</span>{#if !a.read}<span
                  class="unread-dot"
                  aria-label="Unread"
                ></span>{:else}<Check size={11} />{/if}
            </div>
          </button>
        {/each}
        {#if !busy && !items.length}<div class="empty">
            <BookOpen size={28} />
            <h3>
              {query
                ? 'Nothing found'
                : status === 'starred'
                  ? 'A place for the good ones'
                  : library.feeds
                    ? 'You’re all caught up'
                    : 'Your reading room starts here'}
            </h3>
            <p>
              {query
                ? 'Try another phrase or collection.'
                : status === 'starred'
                  ? 'Save an article with the bookmark button or S.'
                  : library.feeds
                    ? 'Choose another collection to keep reading.'
                    : 'Add a feed or import your subscriptions to begin.'}
            </p>
            {#if !library.feeds}<button
                class="add-source"
                onclick={() => (manage = 'add')}>Add your first feed</button
              >{/if}
          </div>{/if}
        {#if next}<button
            class="load-more"
            disabled={busy}
            onclick={() => load(true, true)}
            >{busy ? 'Loading…' : 'Load more articles'}<ArrowDown
              size={14}
            /></button
          >{/if}
        <div class="list-end">
          {items.length} articles loaded <span>·</span>
          {library.feeds} sources in your library
        </div>
      </section>
      <section class="reader-panel" aria-label="Article reader">
        <div class="reader-toolbar">
          <div>
            <button
              class="icon back-to-list"
              aria-label="Back to articles"
              onclick={() => (mobileRead = false)}
              ><ChevronLeft size={18} /></button
            ><span class="reader-label"
              >{focus ? 'FOCUS MODE' : 'THE READING ROOM'}</span
            >
          </div>
          <div class="reader-actions">
            <button
              class="icon"
              disabled={!article}
              aria-label={article?.read ? 'Mark unread' : 'Mark read'}
              title="Toggle read · M"
              onclick={() => article && patch(article, { read: !article.read })}
              ><Check size={17} class={article?.read ? 'accent' : ''} /></button
            ><button
              class="icon"
              disabled={!article}
              aria-label={article?.starred ? 'Unsave article' : 'Save article'}
              title="Save · S"
              onclick={() =>
                article && patch(article, { starred: !article.starred })}
              ><Bookmark
                size={17}
                fill={article?.starred ? 'currentColor' : 'none'}
                class={article?.starred ? 'accent' : ''}
              /></button
            ><span class="separator"></span><button
              class="icon text-size"
              aria-label="Change reading text size"
              title="Cycle text size"
              onclick={() => {
                fontSize = fontSize >= 23 ? 17 : fontSize + 2;
                preference();
              }}><Type size={18} /></button
            ><button
              class="icon"
              aria-label={focus ? 'Exit focus mode' : 'Enter focus mode'}
              title="Focus · Z"
              onclick={() => (focus = !focus)}
              >{#if focus}<Minimize2 size={17} />{:else}<Maximize2
                  size={17}
                />{/if}</button
            >{#if article}<a
                class="icon"
                href={article.url}
                target="_blank"
                rel="noopener noreferrer"
                aria-label="Open original article"
                title="Open original · O"><ArrowUpRight size={19} /></a
              >{/if}
          </div>
        </div>
        <div class="reader-scroll" bind:this={reader}>
          {#if article}<article
              class="story"
              style={'--reading-size:' + fontSize + 'px'}
            >
              <div class="story-kicker">
                <span class="source-badge large"
                  >{article.feedTitle.slice(0, 1).toUpperCase()}</span
                ><span>{article.feedTitle}</span><span class="dot-divider"
                  >/</span
                ><span>{Math.max(1, Math.round(wordCount / 220))} MIN READ</span
                >
              </div>
              <h2>{article.title}</h2>
              <div class="byline">
                {#if article.author}<span>{article.author}</span><span
                    class="dot-divider">·</span
                  >{/if}<time
                  >{new Date(article.publishedAt).toLocaleDateString(
                    undefined,
                    { month: 'long', day: 'numeric', year: 'numeric' }
                  )}</time
                >
              </div>
              {#if article.imageUrl && !failedImages.has(article.id) && !(article.fulltext || article.content || '').includes(article.imageUrl)}<figure
                  class="lead-image"
                >
                  <img
                    src={'/api/image/' + article.id}
                    alt={'Image from ' + article.feedTitle}
                    onerror={() => article && imageFailed(article.id)}
                  />
                  <figcaption>From {article.feedTitle}</figcaption>
                </figure>{/if}
              {#if opening}<div class="loading-text">
                  Opening article…
                </div>{:else}<div class="prose">
                  {@html article.fulltext ||
                    article.content ||
                    '<p>' + plain(article.summary) + '</p>'}
                </div>{/if}
              <div class="story-end">
                <button
                  class="save-end"
                  disabled={opening || !!article.fulltext}
                  onclick={fulltext}
                  ><BookOpen size={15} />{article.fulltext
                    ? 'Full text loaded'
                    : 'Load full article'}</button
                ><span class="end-mark">◈</span>
                <p>You’ve reached the end of this feed entry.</p>
                <a href={article.url} target="_blank" rel="noopener noreferrer"
                  >Continue at {article.feedTitle}<ArrowUpRight size={14} /></a
                ><button
                  class="save-end"
                  onclick={() =>
                    article && patch(article, { starred: !article.starred })}
                  ><Bookmark size={15} />{article.starred
                    ? 'Saved to your library'
                    : 'Keep this for later'}</button
                >
              </div>
            </article>{:else}<div class="empty reader-empty">
              <Radio size={38} />
              <h3>A little space to read.</h3>
              <p>Select a story from your collection.</p>
            </div>{/if}
        </div>
        <footer class="reader-footer">
          <span><span class="live-dot"></span>Shared with terminal</span>
          <div>
            <button
              class="icon"
              aria-label="Previous article"
              disabled={currentIndex <= 0}
              onclick={() => step(-1)}><ArrowUp size={14} /></button
            ><span>{article ? currentIndex + 1 : 0} / {items.length}</span
            ><button
              class="icon"
              aria-label="Next article"
              disabled={currentIndex < 0 || currentIndex >= items.length - 1}
              onclick={() => step(1)}><ArrowDown size={14} /></button
            >
          </div>
        </footer>
      </section>
    </div>
  </main>
  {#if toast}<div class="toast" role="status">
      <Check size={16} />{toast}{#if undoStack.length}<button onclick={undo}
          ><Undo2 size={13} />Undo</button
        >{/if}
    </div>{/if}
  {#if showHelp || showHealth}<div
      class="modal-backdrop"
      role="presentation"
      onclick={(e) => {
        if (e.target === e.currentTarget) {
          showHelp = false;
          showHealth = false;
        }
      }}
    >
      <div
        class="modal"
        use:dialogFocus
        role="dialog"
        aria-modal="true"
        aria-label={showHelp ? 'Keyboard shortcuts' : 'Feed health'}
        tabindex="-1"
      >
        <button
          class="icon modal-close"
          aria-label="Close dialog"
          onclick={() => {
            showHelp = false;
            showHealth = false;
          }}><X size={19} /></button
        >{#if showHelp}<p class="eyebrow">MAKE YOURSELF AT HOME</p>
          <h2>A reader that keeps up.</h2>
          <p>Everything you need, a keystroke away.</p>
          <div class="shortcut-grid">
            {#each [['J / K', 'Next / previous article'], ['S', 'Save for later'], ['M', 'Toggle read status'], ['U', 'Undo last action'], ['/', 'Search articles'], ['Z', 'Focus reading mode'], ['O', 'Open original'], ['R', 'Reload shared state'], ['G', 'Manage feeds'], ['A', 'Add a feed'], ['Esc', 'Close / back']] as [k, label]}<span
                >{label}</span
              ><kbd>{k}</kbd>{/each}
          </div>{:else}<p class="eyebrow">YOUR SOURCES</p>
          <h2>Feed health</h2>
          <p>
            {library.feeds} imported feeds · {library.articles.toLocaleString()} cached
            articles. Feeds update automatically while Current is running.
          </p>
          <div class="health-list">
            {#each feeds as f}<div>
                <strong>{f.title}</strong><span class:feed-error={!!f.lastError}
                  >{f.lastError ||
                    (f.lastFetchedAt
                      ? 'Cached ' + date(f.lastFetchedAt)
                      : 'No cached articles yet')}</span
                >
              </div>{/each}
          </div>{/if}
      </div>
    </div>{/if}
  {#if manage}<FeedManager
      initial={manage}
      onclose={() => (manage = '')}
      onchange={() => {
        managerChanged().catch((e) => (error = String(e)));
      }}
    />{/if}
</div>
