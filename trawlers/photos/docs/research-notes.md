---
written_by: ai
---

# Historical Apple framework research

This is a dated research snapshot from 28 May 2026. It records framework
capabilities, not current Photos product direction. Use
[the architecture](architecture.md) for current decisions and re-check Apple's
documentation before relying on an API detail.

The snapshot found:

- PhotoKit exposes Photos assets, collections, resources, metadata and location
- PhotoKit model objects are read-only; changes require explicit photo-library
  change requests, which photoscrawl does not make
- Vision can analyse images locally for text, barcodes, faces, foreground
  subjects, classification, similarity and quality
- Vision can feed Core ML classifiers

The last 2 capabilities do not make local Vision or Core ML the photoscrawl
classification path. Photos image classification and image-model evals use
frontier models through Ollama Cloud.

Primary references checked for this snapshot:

- [Photos](https://developer.apple.com/documentation/photos)
- [PhotoKit](https://developer.apple.com/documentation/photokit)
- [Fetching assets](https://developer.apple.com/documentation/photokit/fetching-assets)
- [Fetching objects and requesting changes](https://developer.apple.com/documentation/photokit/fetching_objects_and_requesting_changes)
- [PHAsset location](https://developer.apple.com/documentation/photokit/phasset/location)
- [Vision](https://developer.apple.com/documentation/vision)
- [Recognising text in images](https://developer.apple.com/documentation/vision/recognizing-text-in-images)
- [Classifying images with Vision and Core ML](https://developer.apple.com/documentation/coreml/classifying-images-with-vision-and-core-ml)
