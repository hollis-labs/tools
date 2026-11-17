import { defineConfig } from '@playwright/test'

const binary = process.env.PILOT_BIN
const agent = process.env.PILOT_AGENT
const web = process.env.PILOT_WEB
const state = process.env.PILOT_TEST_STATE
if (!binary || !agent || !web || !state) throw new Error('Run through scripts/test.sh to provide the pinned pilot build')

export default defineConfig({
  testDir: 'e2e',
  timeout: 30_000,
  use: { browserName: 'chromium', headless: true, launchOptions: { executablePath: process.env.PILOT_CHROME || '/usr/bin/google-chrome' } },
  webServer: [
    { command: `${binary} -addr 127.0.0.1:7089 -state ${state}/off -agent ${agent} -web ${web}`, url: 'http://127.0.0.1:7089', reuseExistingServer: false, timeout: 10_000 },
    { command: `${binary} -addr 127.0.0.1:7090 -state ${state}/on -agent ${agent} -web ${web} -gate`, url: 'http://127.0.0.1:7090', reuseExistingServer: false, timeout: 10_000 },
  ],
})
