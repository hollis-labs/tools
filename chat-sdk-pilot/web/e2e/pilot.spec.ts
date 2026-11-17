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
  await page.goto(off)
  const controller = page.getByTestId('controller-pane')
  const viewer = page.getByTestId('viewer-pane')
  await expect(controller.getByTestId('role')).toHaveText('controller attached')
  await expect(viewer.getByTestId('role')).toHaveText('viewer detached')
  await controller.locator('textarea').fill('hello')
  await controller.locator('textarea').press('Enter')
  await expect(controller.getByText('Echo: hello')).toBeVisible()

  await viewer.getByRole('button', { name: 'Attach', exact: true }).click()
  await expect(viewer.getByTestId('role')).toHaveText('viewer attached')
  await expect(viewer.getByText('Echo: hello')).toBeVisible()
  await expect(viewer.locator('textarea')).toBeDisabled()

  await viewer.getByRole('button', { name: 'Take over' }).click()
  await expect(viewer.getByTestId('role')).toHaveText('controller attached')
  await viewer.locator('textarea').fill('/tool nonzero')
  await viewer.locator('textarea').press('Enter')
  await expect(viewer.locator('[data-outcome="nonzero_exit"]')).toBeVisible()
  await viewer.locator('textarea').fill('/tool lost')
  await viewer.locator('textarea').press('Enter')
  await expect(viewer.locator('[data-outcome="unknown"]')).toBeVisible()
  await expect(controller.locator('[data-outcome="nonzero_exit"]')).toBeVisible()
  await expect(controller.locator('[data-outcome="unknown"]')).toBeVisible()

  await controller.locator('textarea').fill('stale writer')
  await controller.locator('textarea').press('Enter')
  await expect(controller.getByRole('alert')).toContainText(/lease|stale/i)
  await expect(controller.locator('textarea')).toHaveValue('stale writer')

  await controller.getByRole('button', { name: 'Detach' }).click()
  await viewer.getByRole('button', { name: 'Detach' }).click()
  await expect(viewer.getByTestId('role')).toHaveText('controller detached')
  const saved = await viewer.getByTestId('cursor').textContent()
  await viewer.getByRole('button', { name: 'Attach', exact: true }).click()
  await expect(viewer.getByTestId('role')).toHaveText('controller attached')
  await expect(viewer.getByTestId('cursor')).toHaveText(saved!)
  await expect(viewer.getByText('Tool outcome: unknown')).toBeVisible()

  await viewer.locator('textarea').fill('/gate')
  await viewer.locator('textarea').press('Enter')
  await expect(viewer.getByText('Gatecrasher is off; no request was made.')).toBeVisible()
  const offState = await (await page.request.get(`${off}/api/state`)).json()
  expect(offState.gate_calls).toBe(0)

  await viewer.getByRole('button', { name: 'Detach' }).click()
  await page.reload()
  const replayed = page.getByTestId('controller-pane')
  await expect(replayed.getByTestId('role')).toHaveText('controller attached')
  await expect(replayed.locator('[data-outcome="nonzero_exit"]')).toBeVisible()
  await expect(replayed.locator('[data-outcome="unknown"]')).toBeVisible()

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
  await gate.goto(on)
  const gateController = gate.getByTestId('controller-pane')
  await expect(gateController.getByTestId('role')).toHaveText('controller attached')
  await gateController.locator('textarea').fill('/gate')
  await gateController.locator('textarea').press('Enter')
  await expect(gateController.getByText('Fake Gatecrasher approval')).toBeVisible()
  const onState = await (await gate.request.get(`${on}/api/state`)).json()
  expect(onState.gate_calls).toBeGreaterThan(0)
  expect(transcript(join(state, 'on')).some(row => JSON.stringify(row).includes('pilot-decision-1'))).toBe(true)

  await gate.close()
})
