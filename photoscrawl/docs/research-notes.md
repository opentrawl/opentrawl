# Research Notes

Checked 2026-05-28:

- PhotoKit is Apple's supported API for Photos assets, collections, resources,
  metadata, and location.
- PhotoKit model objects are read-only metadata objects; changes require
  explicit Photo Library change requests, which this crawler must never use.
- Vision provides local image/video analysis for OCR, barcode/QR detection,
  faces, foreground/subject signals, image classification, visual similarity,
  and quality.
- Vision + Core ML can run local classifiers; Apple sample docs use Vision to
  crop/scale images and pass them to Core ML classifiers.

Useful primary docs:

- https://developer.apple.com/documentation/photos
- https://developer.apple.com/documentation/photokit
- https://developer.apple.com/documentation/photokit/fetching-assets
- https://developer.apple.com/documentation/photokit/fetching_objects_and_requesting_changes
- https://developer.apple.com/documentation/photokit/phasset/1624788-location
- https://developer.apple.com/documentation/vision
- https://developer.apple.com/documentation/vision/recognizing-text-in-images
- https://developer.apple.com/documentation/coreml/classifying-images-with-vision-and-core-ml
