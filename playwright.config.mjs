import {defineConfig} from '@playwright/test';

export default defineConfig({
  testDir: './test/e2e',
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  expect: {timeout: 15_000},
  use: {
    browserName: 'chromium',
    headless: true,
    viewport: {width: 1440, height: 900},
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure'
  }
});
