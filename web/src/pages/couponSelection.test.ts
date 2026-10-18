// Runs under `node --test` with Node's built-in type stripping (npm test), so
// it needs no test framework. The module under test is pure on purpose: the
// order form is a .tsx component and type stripping cannot run JSX, so the
// rule that decides whether a previewed coupon still applies lives here where
// it can actually be executed.
import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { activeCoupon, couponMatchesSelection, type PreviewedCoupon } from './couponSelection.ts'

// What /coupons/preview answered for the standard package with two options.
const previewed = (packageId: string, optionIds: string[]): PreviewedCoupon =>
  ({ code: 'WELCOME10', name: '첫 주문 10% 할인', discount_amount: 12800, payable_amount: 115200, package_id: packageId, option_ids: optionIds })

describe('couponMatchesSelection', () => {
  it('holds for the very selection it was priced against', () => {
    assert.equal(couponMatchesSelection(previewed('pkg-standard', ['opt-rush', 'opt-source']), 'pkg-standard', ['opt-rush', 'opt-source']), true)
  })

  it('holds when only the option order differs', () => {
    assert.equal(couponMatchesSelection(previewed('pkg-standard', ['opt-rush', 'opt-source']), 'pkg-standard', ['opt-source', 'opt-rush']), true)
  })

  it('falls away when the package changed', () => {
    assert.equal(couponMatchesSelection(previewed('pkg-standard', ['opt-rush']), 'pkg-premium', ['opt-rush']), false)
  })

  it('falls away when an option was added', () => {
    assert.equal(couponMatchesSelection(previewed('pkg-standard', ['opt-rush']), 'pkg-standard', ['opt-rush', 'opt-source']), false)
  })

  it('falls away when an option was removed', () => {
    assert.equal(couponMatchesSelection(previewed('pkg-standard', ['opt-rush', 'opt-source']), 'pkg-standard', ['opt-rush']), false)
  })

  it('falls away when one option was swapped for another', () => {
    assert.equal(couponMatchesSelection(previewed('pkg-standard', ['opt-rush']), 'pkg-standard', ['opt-source']), false)
  })

  it('holds for a talent with no packages and no options, where both sides are empty', () => {
    assert.equal(couponMatchesSelection(previewed('', []), '', []), true)
  })
})

describe('activeCoupon', () => {
  it('shows nothing when no coupon was applied', () => {
    assert.equal(activeCoupon(null, 'pkg-standard', ['opt-rush']), null)
  })

  it('keeps the coupon while the selection is untouched', () => {
    const coupon = previewed('pkg-standard', ['opt-rush'])
    assert.equal(activeCoupon(coupon, 'pkg-standard', ['opt-rush']), coupon)
  })

  it('drops the coupon the moment the package changes, so the stale payable amount cannot be shown', () => {
    assert.equal(activeCoupon(previewed('pkg-standard', ['opt-rush']), 'pkg-premium', ['opt-rush']), null)
  })

  it('drops the coupon when the chosen options change', () => {
    assert.equal(activeCoupon(previewed('pkg-standard', ['opt-rush']), 'pkg-standard', []), null)
  })
})
