import AppKit
import QuartzCore
import SwiftUI

struct CoreAnimationOrbitHost: NSViewRepresentable {
  let rootView: AnyView
  let contentSize: CGSize
  let motion: OrbitMotion
  let reduceMotion: Bool

  func makeNSView(context: Context) -> OrbitLayerView {
    let view = OrbitLayerView()
    view.update(
      rootView: rootView,
      contentSize: contentSize,
      motion: motion,
      reduceMotion: reduceMotion
    )
    return view
  }

  func updateNSView(_ view: OrbitLayerView, context: Context) {
    view.update(
      rootView: rootView,
      contentSize: contentSize,
      motion: motion,
      reduceMotion: reduceMotion
    )
  }
}

@MainActor
final class OrbitLayerView: NSView {
  private let hostingView = NSHostingView(rootView: AnyView(EmptyView()))
  private var contentSize = CGSize.zero
  private var motion = OrbitMotion(sourceID: "opentrawl")
  private var reduceMotion = false
  private var animationConfiguration: String?

  override var isFlipped: Bool { true }

  override init(frame frameRect: NSRect) {
    super.init(frame: frameRect)
    wantsLayer = true
    layer?.masksToBounds = false
    layer?.backgroundColor = NSColor.clear.cgColor
    addSubview(hostingView)
    hostingView.wantsLayer = true
    hostingView.layer?.masksToBounds = false
    hostingView.layer?.backgroundColor = NSColor.clear.cgColor
    setAccessibilityElement(false)
  }

  @available(*, unavailable)
  required init?(coder: NSCoder) {
    return nil
  }

  func update(
    rootView: AnyView,
    contentSize: CGSize,
    motion: OrbitMotion,
    reduceMotion: Bool
  ) {
    hostingView.rootView = rootView
    if self.contentSize != contentSize || self.motion != motion || self.reduceMotion != reduceMotion
    {
      self.contentSize = contentSize
      self.motion = motion
      self.reduceMotion = reduceMotion
      animationConfiguration = nil
      needsLayout = true
    }
    updateRasterisationScale()
  }

  override func layout() {
    super.layout()
    let targetFrame = CGRect(
      x: bounds.midX - contentSize.width / 2,
      y: bounds.midY - contentSize.height / 2,
      width: contentSize.width,
      height: contentSize.height
    )
    if hostingView.frame != targetFrame {
      CATransaction.begin()
      CATransaction.setDisableActions(true)
      hostingView.frame = targetFrame
      CATransaction.commit()
      animationConfiguration = nil
    }
    configureAnimation()
  }

  override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    animationConfiguration = nil
    updateRasterisationScale()
    configureAnimation()
  }

  override func hitTest(_ point: NSPoint) -> NSView? {
    let transform = hostingView.layer?.presentation()?.transform ?? CATransform3DIdentity
    let adjustedPoint = NSPoint(
      x: point.x - transform.m41,
      y: point.y - transform.m42
    )
    guard hostingView.frame.contains(adjustedPoint) else { return nil }
    return hostingView.hitTest(hostingView.convert(adjustedPoint, from: self))
  }

  private func updateRasterisationScale() {
    let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 2
    hostingView.layer?.contentsScale = scale
    // Freeze the icon, text and material into one retina texture so motion cannot resize or shimmer.
    hostingView.layer?.shouldRasterize = true
    hostingView.layer?.rasterizationScale = scale
    hostingView.layer?.drawsAsynchronously = true
    hostingView.layer?.magnificationFilter = .linear
    hostingView.layer?.minificationFilter = .linear
  }

  private func configureAnimation() {
    guard bounds.width > 0, bounds.height > 0, let target = hostingView.layer else { return }
    let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 2
    let configuration =
      "\(bounds.width):\(bounds.height):\(scale):\(motion.phaseOffset):\(motion.horizontal):"
      + "\(motion.vertical):\(motion.duration):\(reduceMotion)"
    guard animationConfiguration != configuration else { return }
    animationConfiguration = configuration

    target.removeAnimation(forKey: "opentrawl.orbit")
    CATransaction.begin()
    CATransaction.setDisableActions(true)
    target.transform = CATransform3DIdentity
    CATransaction.commit()
    guard !reduceMotion else { return }

    let sampleCount = 720
    let values = (0...sampleCount).map { sample in
      let progress = Double(sample) / Double(sampleCount)
      let angle = (progress + motion.phaseOffset) * 2 * Double.pi
      return NSValue(
        caTransform3D: CATransform3DMakeTranslation(
          CGFloat(cos(angle)) * motion.horizontal,
          CGFloat(sin(angle)) * motion.vertical,
          0
        )
      )
    }
    let animation = CAKeyframeAnimation(keyPath: "transform")
    animation.values = values
    animation.calculationMode = .linear
    animation.timingFunction = CAMediaTimingFunction(name: .linear)
    animation.preferredFrameRateRange = CAFrameRateRange(
      minimum: 60,
      maximum: 120,
      preferred: 120
    )
    animation.duration = motion.duration
    animation.repeatCount = .infinity
    animation.isRemovedOnCompletion = false
    animation.fillMode = .both
    animation.beginTime = target.convertTime(CACurrentMediaTime(), from: nil)
    target.add(animation, forKey: "opentrawl.orbit")
  }
}

struct CoreAnimationNetworkSignals: NSViewRepresentable {
  let segments: [NetworkSegment]
  let reduceMotion: Bool

  func makeNSView(context: Context) -> NetworkSignalLayerView {
    let view = NetworkSignalLayerView()
    view.update(segments: segments, reduceMotion: reduceMotion)
    return view
  }

  func updateNSView(_ view: NetworkSignalLayerView, context: Context) {
    view.update(segments: segments, reduceMotion: reduceMotion)
  }
}

@MainActor
final class NetworkSignalLayerView: NSView {
  private var segments: [NetworkSegment] = []
  private var reduceMotion = false
  private var animationConfiguration: String?
  private var signalLayers: [CALayer] = []

  override var isFlipped: Bool { true }

  override init(frame frameRect: NSRect) {
    super.init(frame: frameRect)
    wantsLayer = true
    layer?.masksToBounds = false
    layer?.isGeometryFlipped = true
    setAccessibilityElement(false)
  }

  @available(*, unavailable)
  required init?(coder: NSCoder) {
    return nil
  }

  func update(segments: [NetworkSegment], reduceMotion: Bool) {
    self.segments = segments
    self.reduceMotion = reduceMotion
    animationConfiguration = nil
    needsLayout = true
  }

  override func layout() {
    super.layout()
    configureSignals()
  }

  override func hitTest(_ point: NSPoint) -> NSView? {
    nil
  }

  private func configureSignals() {
    guard bounds.width > 0, bounds.height > 0, let rootLayer = layer else { return }
    let geometry = segments.map {
      "\(Int($0.start.x * 10)),\(Int($0.start.y * 10)),\(Int($0.end.x * 10)),\(Int($0.end.y * 10))"
    }.joined(separator: ";")
    let configuration = "\(bounds.width):\(bounds.height):\(reduceMotion):\(geometry)"
    guard animationConfiguration != configuration else { return }
    animationConfiguration = configuration

    signalLayers.forEach { $0.removeFromSuperlayer() }
    signalLayers.removeAll()
    let selected = segments.enumerated().filter { $0.offset % 13 == 5 }.prefix(4)
    for (sequence, entry) in selected.enumerated() {
      let signal = CALayer()
      signal.bounds = CGRect(x: 0, y: 0, width: 4.4, height: 4.4)
      signal.cornerRadius = 2.2
      signal.backgroundColor =
        NSColor(
          red: 0.902,
          green: 0.2,
          blue: 0.137,
          alpha: 0.48
        ).cgColor
      signal.position = entry.element.point(at: 0.44)
      rootLayer.addSublayer(signal)
      signalLayers.append(signal)
      guard !reduceMotion else { continue }

      let path = CGMutablePath()
      path.move(to: entry.element.start)
      path.addLine(to: entry.element.end)
      let animation = CAKeyframeAnimation(keyPath: "position")
      animation.path = path
      animation.calculationMode = .paced
      animation.preferredFrameRateRange = CAFrameRateRange(
        minimum: 60,
        maximum: 120,
        preferred: 120
      )
      animation.duration = 4.8 + Double(sequence) * 0.75
      animation.repeatCount = .infinity
      animation.isRemovedOnCompletion = false
      animation.fillMode = .both
      animation.beginTime =
        signal.convertTime(CACurrentMediaTime(), from: nil) - Double(sequence) * 1.1
      signal.add(animation, forKey: "opentrawl.signal")
    }
  }
}
