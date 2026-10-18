// A coupon preview is priced against one exact order shape. `/coupons/preview`
// takes the package and the chosen options and answers with the amount that
// order would be charged, so the answer stops being true the moment the buyer
// picks a different package or ticks another option.
//
// Keeping that answer honest used to be the caller's job: every control on the
// order form was expected to call setCoupon(null) on change. The options
// checkbox and the code field did; the package select did not, and a buyer who
// applied a coupon and then switched packages kept seeing the old package's
// payable amount as the headline "결제 금액" while the list price beside it had
// already moved. Worse, the order request carried that coupon code along, so a
// package cheaper than the coupon's min_order_amount was only refused by the
// server at the final click.
//
// So the invalidation moved out of the callers and into a derived value: the
// preview records which selection produced it, and the form asks here whether
// that selection is still the one on screen. Forgetting to reset a control can
// no longer leave a stale amount on display, because nothing resets anything —
// a coupon that does not match the current selection is simply not shown.
//
// The coupon code string is deliberately not part of the comparison. Editing
// the code is a different kind of change (the buyer is reaching for another
// coupon, not re-shaping the order), and the field clears the preview itself.

/** A coupon preview, together with the order shape it was priced against. */
export type PreviewedCoupon = { code: string; name: string; discount_amount: number; payable_amount: number; package_id: string; option_ids: string[] }

/**
 * Whether a previewed coupon still describes this selection. Options are
 * compared as sets because the order the buyer ticked the checkboxes in is not
 * part of the price: same length plus containment is enough, since the server
 * answers with the ids it was given and the form holds each id at most once.
 */
export function couponMatchesSelection(coupon: PreviewedCoupon, packageId: string, optionIds: string[]): boolean {
  if (coupon.package_id !== packageId) return false
  if (coupon.option_ids.length !== optionIds.length) return false
  return coupon.option_ids.every((id) => optionIds.includes(id))
}

/** The coupon that may be shown and charged for this selection, if any. */
export function activeCoupon(coupon: PreviewedCoupon | null, packageId: string, optionIds: string[]): PreviewedCoupon | null {
  if (!coupon) return null
  return couponMatchesSelection(coupon, packageId, optionIds) ? coupon : null
}
