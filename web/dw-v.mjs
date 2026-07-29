import { chromium } from '@playwright/test';
const b = await chromium.launch();
const issues=[];
for (const page of ['index.html','docs.html#configuration','docs.html#changelog','docs.html#getting-started','docs.html#overview']) {
  for (const w of [360,390,414,768,1024,1280,1440]) {
    const p = await b.newPage({ viewport: { width: w, height: 900 } });
    await p.goto('http://localhost:8691/' + page, { waitUntil: 'networkidle' });
    await p.waitForTimeout(1100);
    const r = await p.evaluate(() => {
      const de = document.documentElement;
      const masked = de.scrollWidth - de.clientWidth;
      document.body.style.overflowX = 'visible';
      const real = document.documentElement.scrollWidth - document.documentElement.clientWidth;
      document.body.style.overflowX = '';
      return { masked, real };
    });
    if (r.masked > 0 || r.real > 0) issues.push(`${page}@${w}: overflow ${r.masked}px (unmasked ${r.real}px)`);
    await p.close();
  }
}
console.log(issues.length ? 'ISSUES:\n'+issues.join('\n') : 'clean — 5 pages × 7 widths, zero overflow even with the body guard removed');
await b.close();
