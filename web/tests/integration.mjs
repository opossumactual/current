import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { mkdtemp, rm, mkdir, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { once } from 'node:events';
import { createHash } from 'node:crypto';

const root = fileURLToPath(new URL('../../', import.meta.url));
const temp = await mkdtemp(join(tmpdir(), 'current-integration-'));
const screenshots = join(root, 'screenshots');
await mkdir(screenshots, { recursive: true });
let revision = 1;
const fixture = createServer((req, res) => {
  if (req.url === '/broken') {
    res.writeHead(500);
    res.end('Temporarily unavailable');
    return;
  }
  if (req.url === '/site') {
    res.setHeader('Content-Type', 'text/html');
    res.end(
      '<html><head><link rel="alternate" type="application/rss+xml" href="/web/rss" title="Field Notes"></head><body>Field Notes</body></html>'
    );
    return;
  }
  res.setHeader('Content-Type', 'application/rss+xml');
  const id = req.url.replaceAll('/', '-');
  const entries = req.url.startsWith('/reading/') ? 125 : revision + 1;
  const galleryContent = req.url.startsWith('/gallery/')
    ? '<figure><img src="https://images.example.test/one-small.png" srcset="https://images.example.test/one-small.png 320w, https://images.example.test/one.png 800w"><figcaption>First gallery image</figcaption></figure><img src="https://images.example.test/two.png" alt="Second gallery image">'
    : '';
  res.end(
    `<?xml version="1.0"?><rss version="2.0"><channel><title>Field Notes</title><link>http://localhost/</link><description>Independent test feed</description>${Array.from({ length: entries }, (_, i) => `<item><guid>${id}-${i}</guid><title>A little perspective ${i}</title><link>http://localhost/story/${i}</link><description><![CDATA[<p>This is a story from a deterministic local feed. There is room to read and think.</p>${galleryContent}]]></description><pubDate>${new Date(Date.now() - i * 3600000).toUTCString()}</pubDate></item>`).join('')}</channel></rss>`
  );
});
await new Promise((r) => fixture.listen(0, '127.0.0.1', r));
const feedBase = `http://127.0.0.1:${fixture.address().port}`;
const reserve = createServer();
await new Promise((r) => reserve.listen(0, '127.0.0.1', r));
const port = reserve.address().port;
await new Promise((r) => reserve.close(r));
const base = `http://127.0.0.1:${port}`;
const server = spawn(
  join(root, 'bin/current-server'),
  [
    '--addr',
    `127.0.0.1:${port}`,
    '--data',
    join(temp, 'library.db'),
    '--poll-interval',
    '1s'
  ],
  { cwd: root, stdio: ['ignore', 'pipe', 'pipe'] }
);
let logs = '';
server.stderr.on('data', (b) => (logs += b.toString()));
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
async function api(path, method = 'GET', body) {
  const response = await fetch(base + '/api/' + path, {
    method,
    ...(body === undefined
      ? {}
      : {
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body)
        })
  });
  assert(
    response.ok,
    `${method} ${path}: ${response.status} ${await (!response.ok ? response.text() : Promise.resolve(''))}`
  );
  return response.status === 204 ? null : response.json();
}
async function until(check) {
  for (let i = 0; i < 150; i++) {
    if (await check()) return;
    await pause(100);
  }
  throw Error('Timed out waiting for condition');
}
let browser, page;
try {
  await until(async () => {
    try {
      return (await fetch(base + '/api/library')).ok;
    } catch {
      return false;
    }
  });
  browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] });
  page = await browser.newPage({
    viewport: { width: 1440, height: 980 }
  });
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(base);
  await page
    .getByText('Your reading room starts here', { exact: true })
    .waitFor();
  await page
    .getByRole('button', { name: 'Add feed', exact: false })
    .first()
    .click();
  const dialog = page.getByRole('dialog', { name: 'Manage feeds' });
  await dialog.waitFor();
  await dialog.getByLabel('Website or feed URL').fill(feedBase + '/site');
  await dialog.getByRole('button', { name: 'Find feeds', exact: true }).click();
  await dialog.getByLabel('Available feeds').waitFor();
  await dialog.getByRole('button', { name: 'Subscribe', exact: true }).click();
  await dialog
    .getByText('Subscribed. Your first stories are ready.', { exact: true })
    .waitFor();
  let feeds = await api('feeds');
  assert.equal(feeds.length, 1);
  const feed = feeds[0];
  assert.equal(feed.unread, 2);
  await dialog
    .getByRole('button', { name: 'Collections', exact: true })
    .click();
  await dialog.getByLabel('New collection name').fill('Weekend');
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();
  await dialog.getByText('Collection created.', { exact: true }).waitFor();
  await dialog.getByLabel('Rename Weekend').fill('Reading');
  await dialog.getByRole('button', { name: 'Save', exact: true }).click();
  await dialog.getByText('Collection renamed.', { exact: true }).waitFor();
  const cat = (await api('categories'))[0];
  await dialog.getByRole('button', { name: /^Feeds/ }).click();
  await dialog.getByRole('button', { name: /Field Notes/ }).click();
  await dialog
    .getByLabel('Feed name', { exact: true })
    .fill('Daily perspective');
  await dialog
    .getByLabel('Collection', { exact: true })
    .selectOption(String(cat.id));
  await dialog.getByLabel('Check every (minutes)').fill('15');
  await dialog.getByLabel('Fetch full text for short entries').check();
  await dialog.getByRole('button', { name: 'Save changes' }).click();
  await dialog.getByText('Feed settings saved.', { exact: true }).waitFor();
  assert.equal((await api('feeds'))[0].categoryId, cat.id);
  await dialog.getByRole('button', { name: 'Pause', exact: true }).click();
  await dialog.getByRole('button', { name: 'Resume', exact: true }).waitFor();
  assert.equal((await api('feeds'))[0].paused, true);
  // Turning off extraction avoids requesting a fake article URL during the live-refresh test.
  await dialog.getByLabel('Fetch full text for short entries').uncheck();
  await dialog.getByRole('button', { name: 'Save changes' }).click();
  await dialog.getByText('Feed settings saved.', { exact: true }).waitFor();
  await page.screenshot({ path: join(screenshots, 'web-feed-manager.png') });
  await dialog.getByRole('button', { name: 'Resume', exact: true }).click();
  await dialog.getByRole('button', { name: 'Pause', exact: true }).waitFor();
  revision = 2;
  await dialog.getByRole('button', { name: 'Update now', exact: true }).click();
  await dialog.getByText(/Updated\. \d+ new articles\./).waitFor();
  await dialog.getByRole('button', { name: 'Close feed manager' }).click();
  await page.locator('.story .prose').waitFor();
  await page.getByRole('button', { name: 'Save article', exact: true }).click();
  await page
    .getByRole('button', { name: 'Unsave article', exact: true })
    .waitFor();
  const saved = (await api('articles?status=starred')).items[0];
  assert(saved);
  await page.keyboard.press('u');
  await page
    .getByRole('button', { name: 'Save article', exact: true })
    .waitFor();
  await page.keyboard.press('s');
  await page
    .getByRole('button', { name: 'Unsave article', exact: true })
    .waitFor();
  const original = await page.locator('.story>h2').textContent();
  await page.keyboard.press('j');
  await page.waitForFunction(
    (t) => document.querySelector('.story>h2')?.textContent !== t,
    original
  );
  await page.keyboard.press('k');
  await page.waitForFunction(
    (t) => document.querySelector('.story>h2')?.textContent === t,
    original
  );
  await page.getByRole('button', { name: 'Use dark theme' }).click();
  assert.equal(await page.locator('.app.dark').count(), 1);
  await page.screenshot({ path: join(screenshots, 'web-reading-dark.png') });
  await page.keyboard.press('g');
  await dialog.waitFor();
  await dialog.getByRole('button', { name: /Daily perspective/ }).click();
  await dialog
    .getByRole('button', { name: 'Unsubscribe', exact: true })
    .click();
  await dialog
    .getByRole('button', { name: 'Confirm unsubscribe', exact: true })
    .click();
  await dialog
    .getByText('Unsubscribed. Articles and saved items kept.', { exact: true })
    .waitFor();
  assert.equal((await api('feeds')).length, 0);
  assert.equal((await api('articles/' + saved.id)).starred, true);
  await dialog.getByLabel('Show unsubscribed feeds').check();
  await dialog.getByRole('button', { name: /Daily perspective/ }).click();
  await dialog
    .getByRole('button', { name: 'Resubscribe', exact: true })
    .click();
  await dialog
    .getByText('Subscription restored. Reading history kept.', { exact: true })
    .waitFor();
  assert.equal((await api('feeds'))[0].id, feed.id);
  // Broken sources show a readable error and can be repaired in place.
  await dialog
    .getByLabel('Feed URL', { exact: true })
    .fill(feedBase + '/broken');
  await dialog.getByRole('button', { name: 'Save changes' }).click();
  await dialog.getByText('Feed settings saved.', { exact: true }).waitFor();
  await dialog.getByRole('button', { name: 'Update now', exact: true }).click();
  await dialog.locator('.problem').waitFor();
  await dialog
    .getByLabel('Feed URL', { exact: true })
    .fill(feedBase + '/web/rss');
  await dialog.getByRole('button', { name: 'Save changes' }).click();
  await dialog.getByText('Feed settings saved.', { exact: true }).waitFor();
  await dialog.getByRole('button', { name: 'Update now', exact: true }).click();
  await page.waitForFunction(() => !document.querySelector('dialog .problem'));
  await dialog
    .getByRole('button', { name: 'Import / export', exact: true })
    .click();
  const opml = `<opml version="2.0"><body><outline text="Imported"><outline text="Second source" type="rss" xmlUrl="${feedBase}/second/rss"/></outline><outline text="Existing" type="rss" xmlUrl="${feedBase}/web/rss"/></body></opml>`;
  await dialog.getByLabel('OPML file', { exact: true }).setInputFiles({
    name: 'subscriptions.opml',
    mimeType: 'text/xml',
    buffer: Buffer.from(opml)
  });
  await dialog
    .getByRole('button', { name: 'Import subscriptions', exact: true })
    .click();
  await dialog
    .getByText('Imported 1 feeds; skipped 1 existing subscriptions.', {
      exact: true
    })
    .waitFor();
  const download = page.waitForEvent('download');
  await dialog.getByRole('link', { name: 'Export OPML' }).click();
  assert.equal((await download).suggestedFilename(), 'current.opml');
  await until(async () => {
    const f = (await api('feeds')).find((f) => f.title === 'Second source');
    return f?.unread === 3;
  });
  // Collection removal retains subscriptions, and the reader exposes Uncategorized.
  await dialog
    .getByRole('button', { name: 'Collections', exact: true })
    .click();
  await dialog
    .getByRole('button', { name: 'Remove collection Reading' })
    .click();
  await dialog
    .getByText('Collection removed. Its feeds are now Uncategorized.', {
      exact: true
    })
    .waitFor();
  assert.equal(
    (await api('feeds')).find((f) => f.id === feed.id).categoryId,
    null
  );
  await dialog.getByRole('button', { name: 'Close feed manager' }).click();
  await page
    .getByRole('textbox', { name: 'Search articles' })
    .fill('zzzz-no-story-987');
  await page.getByText('Nothing found', { exact: true }).waitFor();
  await page.getByRole('button', { name: 'Clear search', exact: true }).click();
  await page.locator('.story .prose').waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Open navigation' }).click();
  await page.getByRole('button', { name: /Manage feeds/ }).click();
  await dialog.waitFor();
  await dialog.getByRole('button', { name: /Daily perspective/ }).click();
  await dialog.getByLabel('Feed name', { exact: true }).waitFor();
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth
    ),
    true
  );
  assert.equal(
    await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth),
    true
  );
  await page.screenshot({ path: join(screenshots, 'web-manager-mobile.png') });
  await dialog.getByRole('button', { name: 'Close feed manager' }).click();
  assert.deepEqual(errors, []);
  console.log(
    'Browser: add/discover, edit, collections, pause/resume, retry, saved history, OPML, reading controls, dark theme and mobile passed.'
  );
  // Record terminal browser launches without opening desktop tabs. This PATH
  // override is confined to the Rust tests, and removed with the test library.
  const openerDir = join(temp, 'opener');
  const openerLog = join(temp, 'opened-links.jsonl');
  await mkdir(openerDir);
  await writeFile(
    join(openerDir, process.platform === 'darwin' ? 'open' : 'xdg-open'),
    `#!/usr/bin/env python3
import json, os, sys
with open(os.environ['CURRENT_TEST_BROWSER_LOG'], 'a') as out:
    out.write(json.dumps(sys.argv[1:]) + '\\n')
sys.exit(1 if '/opener-failure' in sys.argv[-1] else 0)
`,
    { mode: 0o755 }
  );
  const galleryFeed = await api('feeds', 'POST', {
    url: feedBase + '/gallery/rss',
    direct: true
  });
  const galleryPage = await api('articles?feed=' + galleryFeed.id);
  const galleryID = galleryPage.items[0].id;
  const galleryImages = await api('articles/' + galleryID + '/images');
  assert.equal(galleryImages.length, 2);
  assert.equal(galleryImages[0].url, 'https://images.example.test/one.png');
  const galleryCache = ['one', 'two'].map((name) =>
    join(
      temp,
      'images',
      galleryID +
        '-' +
        createHash('sha256')
          .update('https://images.example.test/' + name + '.png')
          .digest('hex')
    )
  );
  const rust = spawn(
    'cargo',
    [
      'test',
      '--offline',
      '--manifest-path',
      join(root, 'tui/Cargo.toml'),
      '--',
      '--ignored',
      '--nocapture',
      '--test-threads=1'
    ],
    {
      cwd: root,
      env: {
        ...process.env,
        PATH: openerDir + ':' + process.env.PATH,
        CURRENT_TEST_BROWSER_LOG: openerLog,
        CURRENT_TEST_URL: base,
        CURRENT_TEST_FEED: feedBase + '/terminal/rss',
        CURRENT_TEST_READING_FEED: feedBase + '/reading',
        CURRENT_TEST_GALLERY_ID: String(galleryID),
        CURRENT_TEST_GALLERY_CACHE: JSON.stringify(galleryCache)
      },
      stdio: 'inherit'
    }
  );
  assert.equal(
    (await once(rust, 'exit'))[0],
    0,
    'Terminal management integration failed'
  );
  const terminalFeeds = await api('feeds');
  assert(
    terminalFeeds.some(
      (f) => f.title === 'Terminal renamed' && f.fetchIntervalMin === 15
    )
  );
  console.log(
    'Terminal changes verified through the same API used by the graphical reader.'
  );
} catch (error) {
  console.error(logs);
  if (page) {
    await page
      .screenshot({ path: join(screenshots, 'integration-failure.png') })
      .catch(() => {});
    console.error(
      (
        await page
          .locator('dialog')
          .ariaSnapshot()
          .catch(() => '')
      )?.slice(0, 5500)
    );
  }
  throw error;
} finally {
  if (browser) await browser.close();
  server.kill('SIGTERM');
  await Promise.race([once(server, 'exit'), pause(6000)]);
  fixture.closeAllConnections();
  await new Promise((r) => fixture.close(r));
  await rm(temp, { recursive: true, force: true });
}
