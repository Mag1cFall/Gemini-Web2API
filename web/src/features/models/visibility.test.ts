import { describe, expect, it } from 'vitest'

import type { Model } from '@/features/models/types'
import { predefinedGemsSelector, toggleHiddenModels } from '@/features/models/visibility'

function model(id: string, extra: Partial<Model> = {}): Model {
  return {
    id,
    display_name: id,
    default: false,
    capabilities: ['generateContent'],
    thinking_levels: [],
    min_context_window: 32768,
    max_context_window: 1048576,
    accounts: ['acc-1'],
    cooling_accounts: [],
    ...extra,
  }
}

describe('toggleHiddenModels', () => {
  const hydrogen = model('gemini-4-hydrogen')
  const canvas = model('gemini-canvas', { tool: 'canvas', hidden: true })
  const guidedLearning = model('gemini-guided-learning', { tool: 'guided_learning', hidden: true })
  const customGem = model('gem-custom-1')
  const chessChamp = model('gem-chess-champ', { hidden: true, predefined_gem: true })
  const codingPartner = model('gem-coding-partner', { hidden: true, predefined_gem: true })
  const all = [hydrogen, canvas, guidedLearning, customGem, chessChamp, codingPartner]

  it('expands predefined-gems when enabling a single Google preset Gem', () => {
    const next = toggleHiddenModels(
      `gemini-canvas,gemini-guided-learning,${predefinedGemsSelector}`,
      codingPartner,
      all,
      true,
    )
    expect(next).toBe('gem-chess-champ,gemini-canvas,gemini-guided-learning')
  })

  it('folds all preset Gems back to predefined-gems when hiding the last open one', () => {
    const next = toggleHiddenModels(
      'gem-chess-champ,gemini-canvas,gemini-guided-learning',
      codingPartner,
      all,
      false,
    )
    expect(next).toBe(`gemini-canvas,gemini-guided-learning,${predefinedGemsSelector}`)
  })

  it('returns empty string when every hidden model is opened', () => {
    const step1 = toggleHiddenModels('gemini-canvas', canvas, all, true)
    expect(step1).toBe('')
  })

  it('adds and removes regular models without touching other hidden entries', () => {
    const closed = toggleHiddenModels(
      `gemini-canvas,${predefinedGemsSelector}`,
      hydrogen,
      all,
      false,
    )
    expect(closed).toBe(`gemini-4-hydrogen,gemini-canvas,${predefinedGemsSelector}`)
    const reopened = toggleHiddenModels(closed, hydrogen, all, true)
    expect(reopened).toBe(`gemini-canvas,${predefinedGemsSelector}`)
  })
})
