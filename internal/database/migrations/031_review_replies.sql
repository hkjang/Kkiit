-- A review was the last word. The buyer describes what happened and the seller,
-- whose livelihood the text affects, has no way to answer it in the place
-- people read it. Every marketplace that treats its sellers seriously gives
-- them a right of reply, and a reply that sits under the review is also the
-- most useful thing a future buyer can read: it shows how the seller handles
-- being unhappy with.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS seller_reply text;
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS seller_replied_at timestamptz;
-- Sellers now answer reviews from one place, so the list is read by seller and
-- filtered on whether an answer is still owed.
CREATE INDEX IF NOT EXISTS reviews_seller_keyset_idx ON reviews(seller_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS reviews_unanswered_idx ON reviews(seller_id) WHERE seller_reply IS NULL;
