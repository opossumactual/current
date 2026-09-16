<script lang="ts">
  import { onMount } from 'svelte';
  import {
    X,
    Plus,
    Search,
    Folder,
    Radio,
    Upload,
    Download,
    RefreshCw,
    Pause,
    Play,
    Archive,
    ArrowLeft
  } from 'lucide-svelte';
  type Feed = {
    id: number;
    title: string;
    url: string;
    categoryId: number | null;
    fetchIntervalMin: number;
    extractFulltext: boolean;
    paused: boolean;
    unsubscribed: boolean;
    lastError: string;
    lastFetchedAt: string | null;
    lastSuccessAt: string | null;
    nextFetchAt: string;
  };
  type Category = { id: number; title: string };
  let {
    initial = 'feeds',
    onclose,
    onchange
  }: { initial?: string; onclose: () => void; onchange: () => void } = $props();
  let tab = $state('feeds'),
    feeds: Feed[] = $state([]),
    categories: Category[] = $state([]),
    selected: Feed | null = $state(null),
    filter = $state(''),
    archived = $state(false),
    busy = $state(false),
    error = $state(''),
    message = $state(''),
    confirm = $state(false);
  let title = $state(''),
    url = $state(''),
    category = $state(''),
    interval = $state(60),
    fulltext = $state(false),
    newCollection = $state('');
  let candidates: { url: string; title: string }[] = $state([]),
    candidate = $state(''),
    discoveryURL = $state(''),
    file: File | undefined = $state();
  const visible = $derived(
    feeds.filter(
      (f) =>
        f.unsubscribed === archived &&
        (f.title + ' ' + f.url).toLowerCase().includes(filter.toLowerCase())
    )
  );
  async function request(path: string, method = 'GET', body?: unknown) {
    const r = await fetch('/api/' + path, {
      method,
      ...(body === undefined
        ? {}
        : body instanceof FormData
          ? { body }
          : {
              headers: {
                'Content-Type':
                  typeof body === 'string' ? 'text/xml' : 'application/json'
              },
              body: typeof body === 'string' ? body : JSON.stringify(body)
            })
    });
    if (!r.ok)
      throw Error(
        (await r.json().catch(() => ({ error: r.statusText }))).error
      );
    return r.status === 204 ? null : r.json();
  }
  async function reload() {
    [feeds, categories] = await Promise.all([
      request('feeds?includeArchived=1'),
      request('categories')
    ]);
    if (selected) {
      const f = feeds.find((f) => f.id === selected?.id);
      if (f) edit(f);
    }
  }
  async function run(action: () => Promise<void>) {
    if (busy) return;
    busy = true;
    error = '';
    message = '';
    try {
      await action();
    } catch (e) {
      error = String(e);
    } finally {
      busy = false;
    }
  }
  function edit(f: Feed) {
    selected = f;
    title = f.title;
    url = f.url;
    category = String(f.categoryId ?? '');
    interval = f.fetchIntervalMin;
    fulltext = f.extractFulltext;
    confirm = false;
  }
  async function changed(text: string) {
    await reload();
    onchange();
    message = text;
  }
  async function save() {
    await request('feeds/' + selected!.id, 'PATCH', {
      title,
      url,
      categoryId: category ? Number(category) : null,
      fetchIntervalMin: Number(interval),
      extractFulltext: fulltext
    });
    await changed('Feed settings saved.');
  }
  async function discover() {
    candidates = [];
    candidate = '';
    candidates = await request(
      'discover?url=' + encodeURIComponent(discoveryURL)
    );
    candidate = candidates[0]?.url || '';
    if (!candidate)
      throw Error('No feeds found. Paste the RSS or Atom URL to try again.');
  }
  async function subscribe() {
    const found = candidates.find((c) => c.url === candidate);
    const f = await request('feeds', 'POST', {
      url: candidate,
      title: found?.title || '',
      categoryId: category ? Number(category) : null,
      direct: true
    });
    tab = 'feeds';
    archived = false;
    edit(f);
    discoveryURL = '';
    candidates = [];
    await changed(
      f.lastError
        ? 'Subscribed; check the feed status below.'
        : 'Subscribed. Your first stories are ready.'
    );
  }
  function modal(node: HTMLDialogElement) {
    node.showModal();
    return {
      destroy() {
        node.close();
      }
    };
  }
  function stamp(value: string | null) {
    return value ? new Date(value).toLocaleString() : 'Not yet';
  }
  onMount(() => {
    tab = initial;
    run(reload);
  });
</script>

<dialog
  use:modal
  oncancel={(e) => {
    e.preventDefault();
    onclose();
  }}
  aria-label="Manage feeds"
>
  <header>
    <div>
      <p class="eyebrow">MAKE IT YOURS</p>
      <h2>Your sources.</h2>
      <p>A good reading room starts with what you let in.</p>
    </div>
    <button class="icon" aria-label="Close feed manager" onclick={onclose}
      ><X size={20} /></button
    >
  </header>
  <nav aria-label="Feed management">
    <button
      class:active={tab === 'feeds'}
      onclick={() => {
        tab = 'feeds';
        confirm = false;
      }}
      ><Radio size={16} />Feeds
      <small>{feeds.filter((f) => !f.unsubscribed).length}</small></button
    ><button
      class:active={tab === 'add'}
      onclick={() => {
        tab = 'add';
        category = '';
      }}><Plus size={16} />Add feed</button
    ><button
      class:active={tab === 'collections'}
      onclick={() => (tab = 'collections')}
      ><Folder size={16} />Collections</button
    ><button class:active={tab === 'opml'} onclick={() => (tab = 'opml')}
      ><Upload size={16} />Import / export</button
    >
  </nav>
  {#if error}<div class="feedback bad" role="alert">{error}</div>{/if}
  {#if message}<div class="feedback" role="status">{message}</div>{/if}
  {#if busy}<div class="working" role="status">Working…</div>{/if}
  <div class="content" aria-busy={busy}>
    {#if tab === 'feeds'}
      <div class="manager-grid" class:has-selection={!!selected}>
        <section class="source-list">
          <label class="search-field"
            ><Search size={16} /><input
              aria-label="Find a feed"
              placeholder="Find a source…"
              bind:value={filter}
            /></label
          ><label class="check"
            ><input
              type="checkbox"
              bind:checked={archived}
              onchange={() => (selected = null)}
            />Show unsubscribed feeds</label
          >
          <div class="feed-list">
            {#each visible as f}<button
                class:selected={selected?.id === f.id}
                onclick={() => edit(f)}
                ><strong>{f.title || f.url}</strong><span
                  >{f.unsubscribed
                    ? 'Unsubscribed'
                    : f.paused
                      ? 'Paused'
                      : f.lastError
                        ? 'Needs attention'
                        : f.lastSuccessAt
                          ? 'Up to date'
                          : 'Awaiting first update'}</span
                ></button
              >{/each}{#if !visible.length}<p class="hint">
                {archived
                  ? 'No unsubscribed feeds.'
                  : 'No feeds here yet. Add a source or import an OPML file.'}
              </p>{/if}
          </div>
        </section>
        <section class="feed-detail">
          {#if selected}
            <button class="back" onclick={() => (selected = null)}
              ><ArrowLeft size={15} />All feeds</button
            >
            <form
              onsubmit={(e) => {
                e.preventDefault();
                run(save);
              }}
            >
              <h3>{selected.title || 'Feed settings'}</h3>
              <label>Feed name<input required bind:value={title} /></label
              ><label
                >Feed URL<input required type="url" bind:value={url} /></label
              >
              <div class="two">
                <label
                  >Collection<select
                    aria-label="Collection"
                    bind:value={category}
                    ><option value="">Uncategorized</option
                    >{#each categories as c}<option value={String(c.id)}
                        >{c.title}</option
                      >{/each}</select
                  ></label
                ><label
                  >Check every (minutes)<input
                    required
                    type="number"
                    min="5"
                    max="1440"
                    bind:value={interval}
                  /></label
                >
              </div>
              <label class="check"
                ><input type="checkbox" bind:checked={fulltext} />Fetch full
                text for short entries</label
              ><button class="primary" disabled={busy}>Save changes</button>
            </form>
            <div class="health">
              <h4>
                {selected.unsubscribed
                  ? 'Unsubscribed'
                  : selected.paused
                    ? 'Updates paused'
                    : 'Feed status'}
              </h4>
              <dl>
                <dt>Last attempt</dt>
                <dd>{stamp(selected.lastFetchedAt)}</dd>
                <dt>Last successful update</dt>
                <dd>{stamp(selected.lastSuccessAt)}</dd>
                <dt>Next check</dt>
                <dd>
                  {selected.paused || selected.unsubscribed
                    ? 'Off'
                    : new Date(selected.nextFetchAt).getTime() < Date.now()
                      ? 'Queued'
                      : stamp(selected.nextFetchAt)}
                </dd>
              </dl>
              {#if selected.lastError}<p class="problem">
                  {selected.lastError}
                </p>{/if}
              <div class="actions">
                {#if selected.unsubscribed}<button
                    disabled={busy}
                    onclick={() =>
                      run(async () => {
                        await request('feeds/' + selected!.id, 'PATCH', {
                          unsubscribed: false,
                          paused: false
                        });
                        archived = false;
                        await changed(
                          'Subscription restored. Reading history kept.'
                        );
                      })}><Play size={15} />Resubscribe</button
                  >{:else}<button
                    disabled={busy}
                    onclick={() =>
                      run(async () => {
                        const result = await request(
                          'feeds/' + selected!.id + '/refresh',
                          'POST'
                        );
                        await changed(
                          result.feed.lastError
                            ? 'The feed could not be updated. See the error below.'
                            : `Updated. ${result.inserted} new articles.`
                        );
                      })}><RefreshCw size={15} />Update now</button
                  ><button
                    disabled={busy}
                    onclick={() =>
                      run(async () => {
                        await request('feeds/' + selected!.id, 'PATCH', {
                          paused: !selected!.paused
                        });
                        await changed('Update preference saved.');
                      })}
                    >{#if selected.paused}<Play size={15} />Resume{:else}<Pause
                        size={15}
                      />Pause{/if}</button
                  >{/if}
              </div>
            </div>
            {#if !selected.unsubscribed}<div class="unsubscribe">
                {#if confirm}<p>
                    Unsubscribe from <strong>{selected.title}</strong>? Your
                    articles and saved items will stay in your library.
                  </p>
                  <div class="actions">
                    <button
                      class="danger"
                      disabled={busy}
                      onclick={() =>
                        run(async () => {
                          await request('feeds/' + selected!.id, 'DELETE');
                          selected = null;
                          await changed(
                            'Unsubscribed. Articles and saved items kept.'
                          );
                        })}>Confirm unsubscribe</button
                    ><button onclick={() => (confirm = false)}>Cancel</button>
                  </div>{:else}<button
                    class="quiet"
                    onclick={() => (confirm = true)}
                    ><Archive size={15} />Unsubscribe</button
                  >{/if}
              </div>{/if}
          {:else}<div class="placeholder">
              <Radio size={32} />
              <h3>A place for every perspective.</h3>
              <p>
                Choose a feed to organize it, change its schedule, or check its
                health.
              </p>
              <button class="primary" onclick={() => (tab = 'add')}
                ><Plus size={16} />Add your next source</button
              >
            </div>{/if}
        </section>
      </div>
    {:else if tab === 'add'}
      <section class="single">
        <span class="feature-icon"><Plus size={25} /></span>
        <h3>Follow something good.</h3>
        <p>
          Paste a website address or an RSS, Atom, or JSON feed URL. We’ll find
          the available feeds.
        </p>
        <form
          onsubmit={(e) => {
            e.preventDefault();
            run(discover);
          }}
        >
          <label
            >Website or feed URL<input
              required
              placeholder="https://example.com"
              bind:value={discoveryURL}
            /></label
          ><button class="primary" disabled={busy}>Find feeds</button>
        </form>
        {#if candidates.length}<form
            class="found"
            onsubmit={(e) => {
              e.preventDefault();
              run(subscribe);
            }}
          >
            <label
              >Available feeds<select bind:value={candidate}
                >{#each candidates as c}<option value={c.url}
                    >{c.title || c.url}</option
                  >{/each}</select
              ></label
            >
            <p class="hint url">{candidate}</p>
            <label
              >Add to collection<select bind:value={category}
                ><option value="">Uncategorized</option
                >{#each categories as c}<option value={String(c.id)}
                    >{c.title}</option
                  >{/each}</select
              ></label
            ><button class="primary" disabled={busy}
              ><Plus size={16} />Subscribe</button
            >
          </form>{/if}
      </section>
    {:else if tab === 'collections'}
      <section class="single wide">
        <h3>A little order goes a long way.</h3>
        <p>Group sources by topic, mood, or whatever makes sense to you.</p>
        <form
          class="inline"
          onsubmit={(e) => {
            e.preventDefault();
            run(async () => {
              await request('categories', 'POST', { title: newCollection });
              newCollection = '';
              await changed('Collection created.');
            });
          }}
        >
          <input
            required
            aria-label="New collection name"
            placeholder="New collection name"
            bind:value={newCollection}
          /><button class="primary" disabled={busy}
            ><Plus size={16} />Create</button
          >
        </form>
        <div class="collection-list">
          {#each categories as c}<form
              class="collection-edit"
              onsubmit={(e) => {
                e.preventDefault();
                const data = new FormData(e.currentTarget);
                run(async () => {
                  await request('categories/' + c.id, 'PATCH', {
                    title: data.get('title')
                  });
                  await changed('Collection renamed.');
                });
              }}
            >
              <Folder size={17} /><input
                required
                name="title"
                aria-label={'Rename ' + c.title}
                value={c.title}
              /><small
                >{feeds.filter((f) => !f.unsubscribed && f.categoryId === c.id)
                  .length} feeds</small
              ><button disabled={busy}>Save</button><button
                type="button"
                disabled={busy}
                aria-label={'Remove collection ' + c.title}
                onclick={() =>
                  run(async () => {
                    await request('categories/' + c.id, 'DELETE');
                    await changed(
                      'Collection removed. Its feeds are now Uncategorized.'
                    );
                  })}><X size={16} /></button
              >
            </form>{/each}
        </div>
        <p class="hint">
          Removing a collection moves its feeds to Uncategorized. It keeps all
          subscriptions and articles.
        </p>
      </section>
    {:else}
      <section class="single">
        <span class="feature-icon"><Upload size={25} /></span>
        <h3>Bring your reading list.</h3>
        <p>
          Import an OPML subscription file from FreshRSS or another reader.
          Existing subscriptions are skipped; unsubscribed feeds are restored.
        </p>
        <form
          onsubmit={(e) => {
            e.preventDefault();
            run(async () => {
              if (!file) throw Error('Choose an OPML file first.');
              const data = new FormData();
              data.append('file', file);
              const result = await request('opml/import', 'POST', data);
              await changed(
                `Imported ${result.added} feeds; skipped ${result.skipped} existing subscriptions.`
              );
            });
          }}
        >
          <label
            >OPML file<input
              type="file"
              accept=".opml,.xml,text/xml"
              required
              onchange={(e) => (file = e.currentTarget.files?.[0])}
            /></label
          ><button class="primary" disabled={busy || !file}
            ><Upload size={16} />Import subscriptions</button
          >
        </form>
        <div class="export">
          <h3>Take your sources with you.</h3>
          <p>
            Export active subscriptions and collections. OPML contains feed
            addresses, not article history or read/save state.
          </p>
          <a class="button" href="/api/opml/export" download="current.opml"
            ><Download size={16} />Export OPML</a
          >
        </div>
      </section>
    {/if}
  </div>
  <footer>
    <span>One library. Both ways to read.</span><button onclick={onclose}
      >Back to reading</button
    >
  </footer>
</dialog>

<style>
  dialog {
    color: var(--ink);
    background: var(--bg);
    border: 1px solid var(--line);
    border-radius: 18px;
    padding: 0;
    width: min(960px, 95vw);
    max-height: 92dvh;
    box-shadow: 0 30px 120px #0007;
    overflow: auto;
  }
  dialog::backdrop {
    background: #101c2588;
    backdrop-filter: blur(5px);
  }
  header {
    padding: 28px 32px 22px;
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
  }
  h2 {
    font-family: 'Newsreader Variable', serif;
    font-size: 36px;
    margin: 4px 0 8px;
    font-weight: 500;
  }
  h3 {
    font-family: 'Newsreader Variable', serif;
    font-weight: 500;
    font-size: 25px;
    margin: 0 0 12px;
  }
  p {
    color: var(--muted);
    font-size: 14px;
    line-height: 1.6;
    margin: 0 0 18px;
  }
  header p {
    margin: 0;
  }
  .eyebrow {
    font-size: 10px;
    letter-spacing: 2px;
    color: var(--accent);
  }
  nav {
    padding: 0 28px;
    display: flex;
    gap: 6px;
    border-bottom: 1px solid var(--line);
    overflow: auto;
  }
  button,
  a.button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    border: 1px solid var(--line);
    border-radius: 7px;
    padding: 9px 12px;
    font-size: 12px;
    color: var(--ink);
    background: transparent;
    cursor: pointer;
    text-decoration: none;
  }
  button:hover,
  a.button:hover {
    background: var(--wash);
  }
  button:disabled {
    opacity: 0.5;
    cursor: wait;
  }
  nav button {
    white-space: nowrap;
    border: 0;
    border-bottom: 2px solid transparent;
    border-radius: 0;
    padding: 14px 12px;
    color: var(--muted);
  }
  nav button.active {
    border-color: var(--accent);
    color: var(--accent);
  }
  small {
    font-size: 11px;
    color: var(--muted);
  }
  .content {
    min-height: 440px;
  }
  label {
    display: flex;
    flex-direction: column;
    gap: 8px;
    font-size: 12px;
    color: var(--muted);
    margin-bottom: 17px;
  }
  input,
  select {
    width: 100%;
    min-width: 0;
    box-sizing: border-box;
    border: 1px solid var(--line);
    background: var(--panel);
    color: var(--ink);
    border-radius: 7px;
    padding: 10px 11px;
    font: inherit;
    font-size: 13px;
  }
  input:focus,
  select:focus {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .check {
    flex-direction: row;
    align-items: center;
    font-size: 12px;
    line-height: 1.4;
  }
  .check input {
    width: 15px;
    accent-color: var(--accent);
  }
  .primary {
    background: var(--accent);
    border-color: var(--accent);
    color: white;
  }
  .primary:hover {
    filter: brightness(1.08);
    background: var(--accent);
  }
  .manager-grid {
    display: grid;
    grid-template-columns: 290px minmax(0, 1fr);
  }
  .source-list {
    padding: 22px 18px;
    border-right: 1px solid var(--line);
  }
  .search-field {
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 6px;
  }
  .feed-list {
    max-height: 440px;
    overflow: auto;
  }
  .feed-list button {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 5px;
    text-align: left;
    width: 100%;
    border-color: transparent;
    padding: 11px;
    margin: 2px 0;
  }
  .feed-list button.selected {
    background: var(--wash);
    border-color: var(--line);
  }
  .feed-list strong {
    font-size: 13px;
    overflow-wrap: anywhere;
  }
  .feed-list span {
    font-size: 11px;
    color: var(--muted);
  }
  .feed-detail {
    padding: 26px 30px;
  }
  .two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
  }
  .health {
    border-top: 1px solid var(--line);
    margin-top: 24px;
    padding-top: 18px;
  }
  .health h4 {
    font-size: 12px;
    margin: 0 0 12px;
  }
  dl {
    display: grid;
    grid-template-columns: 1fr 1.4fr;
    gap: 7px;
    font-size: 11px;
    line-height: 1.5;
  }
  dt {
    color: var(--muted);
  }
  dd {
    margin: 0;
  }
  .problem {
    background: #d5701715;
    color: var(--accent);
    padding: 12px;
    font-size: 12px;
    overflow-wrap: anywhere;
  }
  .actions {
    display: flex;
    gap: 10px;
    flex-wrap: wrap;
  }
  .unsubscribe {
    border-top: 1px solid var(--line);
    margin-top: 20px;
    padding-top: 16px;
  }
  .quiet {
    border: 0;
    padding-left: 0;
    color: var(--muted);
  }
  .danger {
    color: #d65842;
    border-color: #d65842;
  }
  .placeholder {
    min-height: 340px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    text-align: center;
    color: var(--accent);
  }
  .placeholder h3 {
    color: var(--ink);
    margin-top: 18px;
  }
  .single {
    max-width: 560px;
    margin: auto;
    padding: 34px;
  }
  .wide {
    max-width: 800px;
  }
  .feature-icon {
    color: var(--accent);
    display: block;
    margin-bottom: 20px;
  }
  .found,
  .export {
    margin-top: 28px;
    padding-top: 24px;
    border-top: 1px solid var(--line);
  }
  .hint {
    font-size: 12px;
    margin-top: 12px;
  }
  .url {
    overflow-wrap: anywhere;
  }
  .inline,
  .collection-edit {
    display: flex;
    gap: 10px;
    align-items: center;
  }
  .collection-list {
    margin-top: 20px;
  }
  .collection-edit {
    padding: 10px 0;
    border-bottom: 1px solid var(--line);
  }
  .collection-edit input {
    flex: 1;
  }
  .collection-edit small {
    white-space: nowrap;
  }
  .collection-edit button {
    flex-shrink: 0;
  }
  .feedback {
    padding: 12px 30px;
    background: #41976418;
    font-size: 13px;
    line-height: 1.5;
  }
  .feedback.bad {
    background: #d658421a;
    color: #cf674a;
  }
  .working {
    padding: 8px 30px;
    color: var(--muted);
    font-size: 12px;
  }
  footer {
    border-top: 1px solid var(--line);
    padding: 16px 28px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    font-size: 11px;
    color: var(--muted);
  }
  .back {
    display: none;
  }
  :global(.dark) dialog {
    color-scheme: dark;
  }
  @media (max-width: 650px) {
    dialog {
      width: 96vw;
      max-height: 95dvh;
    }
    header {
      padding: 22px;
    }
    nav {
      padding: 0 10px;
    }
    .manager-grid {
      grid-template-columns: 1fr;
    }
    .source-list {
      border: 0;
    }
    .feed-detail {
      display: none;
      padding: 22px;
    }
    .has-selection .source-list {
      display: none;
    }
    .has-selection .feed-detail {
      display: block;
    }
    .back {
      display: flex;
      margin-bottom: 18px;
    }
    .single {
      padding: 25px;
    }
    .collection-edit {
      gap: 5px;
    }
    .collection-edit small {
      display: none;
    }
    .collection-edit button {
      padding: 8px;
    }
    .two {
      grid-template-columns: 1fr;
    }
    footer {
      padding: 15px;
    }
    footer span {
      max-width: 120px;
    }
    .feed-list {
      max-height: 50dvh;
    }
  }
</style>
