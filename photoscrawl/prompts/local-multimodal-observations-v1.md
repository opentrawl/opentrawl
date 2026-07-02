Return only valid compact JSON. Do not use markdown fences.

Describe only visible evidence in this user-owned photo. Use candidates, not
truth.

Keys:

- scene_summary: one useful sentence.
- visible_text_summary: short visible text summary or null.
- place_candidates: array of candidate places or venue types.
- landmark_candidates: array of candidate landmarks or distinctive structures.
- merchant_or_venue_candidates: array of visible merchant or venue candidates.
- food_or_objects: array of important visible foods, objects, documents,
  vehicles, signs, screens, or activities.
- people_presence: anonymous count/body-part/group description; no identity.
- privacy_sensitivity: array of visible sensitivity hints such as faces,
  document, receipt, address, payment, health, child, license_plate, location.
- cluster_terms: array of normalized terms useful for later clustering/search.
- uncertainties: array of important uncertainties or likely hallucination risks.
