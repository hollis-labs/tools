import { test, expect } from '@playwright/test'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

const off = 'http://127.0.0.1:7089'
const on = 'http://127.0.0.1:7090'

const lines = (path: string) => readFileSync(path, 'utf8').trim().split('\n').map(line => JSON.parse(line))
const transcript = (state: string) => {
  const root = join(state, 'transcripts')
  const hash = readdirSync(root)[0]!
  const dir = join(root, hash)
  return readdirSync(dir).filter(name => name.endsWith('.jsonl')).flatMap(name => lines(join(dir, name)))
}

test('views, tool outcomes and opt-in fake gate share durable evidence', async ({ browser, page }) => {
  const state = process.env.PILOT_TEST_STATE!
  await page.goto(`${off}/?role=controller`)
  await expect(page.getByTestId('role')).toHaveText('controller attached')
  await page.locator('textarea').fill('hello')
  await page.locator('textarea').press('Enter')
  await expect(page.getByText('Echo: hello')).toBeVisible()

  const viewer = await browser.newPage()
  await viewer.goto(`${off}/?role=viewer&after=1`)
  await expect(viewer.getByTestId('role')).toHaveText('viewer attached')
  await expect(viewer.getByText('Echo: hello')).toBeVisible()
  await expect(viewer.locator('textarea')).toBeDisabled()

  const other = await browser.newPage()
  await other.goto(`${off}/?role=controller`)
  await expect(other.getByTestId('role')).toHaveText('controller attached')
  await other.getByRole('button', { name: 'Take over' }).click()
  await other.locator('textarea').fill('/tool nonzero')
  await other.locator('textarea').press('Enter')
  await expect(other.locator('[data-outcome="nonzero_exit"]')).toBeVisible()
  await other.locator('textarea').fill('/tool lost')
  await other.locator('textarea').press('Enter')
  await expect(other.locator('[data-outcome="unknown"]')).toBeVisible()

  await page.locator('textarea').fill('stale writer')
  await page.locator('textarea').press('Enter')
  await expect(page.getByRole('alert')).toContainText(/lease|stale/i)

  await page.getByRole('button', { name: 'Detach' }).click()
  await viewer.getByRole('button', { name: 'Detach' }).click()
  await other.getByRole('button', { name: 'Detach' }).click()
  await expect(other.getByTestId('role')).toHaveText('controller detached')
  const before = other.getByTestId('cursor')
  const saved = await before.textContent()
  await other.getByRole('button', { name: 'Attach', exact: true }).click()
  await expect(other.getByTestId('role')).toHaveText('controller attached')
  await expect(other.getByTestId('cursor')).toHaveText(saved!)
  await expect(other.getByText('Tool outcome: unknown')).toBeVisible()

  await other.locator('textarea').fill('/gate')
  await other.locator('textarea').press('Enter')
  await expect(other.getByText('Gatecrasher is off; no request was made.')).toBeVisible()
  const offState = await (await other.request.get(`${off}/api/state`)).json()
  expect(offState.gate_calls).toBe(0)

  const invocations = lines(join(state, 'off', 'invocations.jsonl'))
  const records = transcript(join(state, 'off'))
  const toolUses = records.flatMap(row => row.message?.content || []).filter((part: any) => part.type === 'tool_use')
  const toolResults = records.flatMap(row => row.message?.content || []).filter((part: any) => part.type === 'tool_result')
  expect(toolUses).toHaveLength(2)
  for (const use of toolUses) {
    expect(invocations.some(record => record.ids.call_id === use.id && record.ids.root_call_id === use.id)).toBe(true)
    expect(invocations.some(record => record.ids.parent_call_id === use.id)).toBe(true)
    expect(toolResults.some((part: any) => part.tool_use_id === use.id)).toBe(true)
  }
  const outcomes = toolResults.map((part: any) => JSON.parse(part.content).outcome)
  expect(outcomes).toContain('nonzero_exit')
  expect(outcomes).toContain('unknown')

  const gate = await browser.newPage()
  await gate.goto(`${on}/?role=controller`)
  await expect(gate.getByTestId('role')).toHaveText('controller attached')
  await gate.locator('textarea').fill('/gate')
  await gate.locator('textarea').press('Enter')
  await expect(gate.getByText('Fake Gatecrasher approval')).toBeVisible()
  const onState = await (await gate.request.get(`${on}/api/state`)).json()
  expect(onState.gate_calls).toBeGreaterThan(0)
  expect(transcript(join(state, 'on')).some(row => JSON.stringify(row).includes('pilot-decision-1'))).toBe(true)

  await viewer.close()
  await other.close()
  await gate.close()
})
