#!/usr/bin/env node
import {initWasm, Resvg} from '@resvg/resvg-wasm';
import {optimize} from 'svgo';
import {readFile, writeFile} from 'node:fs/promises';
import {argv, exit} from 'node:process';
import {html} from '../web_src/js/utils/html.ts';

function pngToSvg(pngBytes: Uint8Array) {
  const pngBase64 = Buffer.from(pngBytes).toString('base64');
  return html`<?xml version="1.0" encoding="UTF-8"?><svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 512 512"><image href="data:image/png;base64,${pngBase64}" width="512" height="512"/></svg>`;
}

async function readFileOrNull(path: URL) {
  try {
    return await readFile(path);
  } catch {
    return null;
  }
}

async function generate(svg: string, path: string, {size, bg}: {size: number, bg?: boolean}) {
  const outputFile = new URL(path, import.meta.url);

  if (String(outputFile).endsWith('.svg')) {
    const {data} = optimize(svg, {
      plugins: [
        'preset-default',
        'removeDimensions',
        {
          name: 'addAttributesToSVGElement',
          params: {
            attributes: [{width: String(size)}, {height: String(size)}],
          },
        },
      ],
    });
    await writeFile(outputFile, data);
    return;
  }

  const resvgJS = new Resvg(svg, {
    fitTo: {
      mode: 'width',
      value: size,
    },
    ...(bg && {background: 'white'}),
  });
  const renderedImage = resvgJS.render();
  const pngBytes = renderedImage.asPng();
  await writeFile(outputFile, Buffer.from(pngBytes));
}

async function main() {
  const gitea = argv.slice(2).includes('gitea');
  const logoPng = await readFileOrNull(new URL('../assets/logo.png', import.meta.url));
  const faviconPng = await readFileOrNull(new URL('../assets/favicon.png', import.meta.url));
  const logoSvg = logoPng ? pngToSvg(logoPng) : await readFile(new URL('../assets/logo.svg', import.meta.url), 'utf8');
  const faviconSvg = faviconPng ? pngToSvg(faviconPng) : await readFile(new URL('../assets/favicon.svg', import.meta.url), 'utf8');
  await initWasm(await readFile(new URL(import.meta.resolve('@resvg/resvg-wasm/index_bg.wasm'))));

  await Promise.all([
    generate(logoSvg, '../public/assets/img/logo.svg', {size: 32}),
    generate(logoSvg, '../public/assets/img/logo.png', {size: 512}),
    generate(faviconSvg, '../public/assets/img/favicon.svg', {size: 32}),
    generate(faviconSvg, '../public/assets/img/favicon.png', {size: 180}),
    generate(faviconSvg, '../public/assets/img/favicon-16.png', {size: 16}),
    generate(faviconSvg, '../public/assets/img/favicon-32.png', {size: 32}),
    generate(faviconSvg, '../public/assets/img/favicon-48.png', {size: 48}),
    generate(faviconSvg, '../public/assets/img/favicon-64.png', {size: 64}),
    generate(logoSvg, '../public/assets/img/avatar_default.png', {size: 200}),
    generate(logoSvg, '../public/assets/img/apple-touch-icon.png', {size: 180, bg: true}),
    generate(logoSvg, '../public/assets/img/icon-192.png', {size: 192}),
    generate(logoSvg, '../public/assets/img/icon-512.png', {size: 512}),
    gitea && generate(logoSvg, '../public/assets/img/gitea.svg', {size: 32}),
  ]);
}

try {
  await main();
} catch (err) {
  console.error(err);
  exit(1);
}
