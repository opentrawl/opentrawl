import Foundation
import TrawlClient

public struct ArchiveTimelineAxisTick: Sendable, Equatable, Identifiable {
  public let id: String
  public let label: String
  public let normalizedPosition: Double
  public let isToday: Bool

  public init(id: String, label: String, normalizedPosition: Double, isToday: Bool = false) {
    self.id = id
    self.label = label
    self.normalizedPosition = normalizedPosition
    self.isToday = isToday
  }
}

public enum ArchiveTimelineIntensityScale {
  public static let legendReferenceCounts: [UInt64] = [10, 1_000, 10_000]
  public static let referenceCeilingCount: Double = 10_000

  /// Shared squared-log intensity in [0, 1] used identically by every monthly
  /// timeline bar and by the 10 / 1k / 10k legend. Empty months always return 0.
  public static func normalizedStrength(forRecordCount count: UInt64) -> Double {
    guard count > 0 else { return 0 }
    let ratio = min(
      1.0,
      log10(Double(count) + 1.0) / log10(referenceCeilingCount + 1.0)
    )
    return ratio * ratio
  }

  public static func barHeightFraction(forRecordCount count: UInt64) -> Double {
    guard count > 0 else { return 0 }
    return 0.30 + 0.70 * normalizedStrength(forRecordCount: count)
  }

  public static func fillOpacity(forRecordCount count: UInt64) -> Double {
    guard count > 0 else { return 0 }
    return 0.26 + 0.68 * normalizedStrength(forRecordCount: count)
  }
}

extension ArchiveMonthlyHistory {
  public var totalMeasuredRecordCount: UInt64 {
    monthlyRecordCountsInChronologicalOrder.reduce(undatedRecordCount) { sum, bucket in
      sum &+ bucket.recordCount
    }
  }
}

public struct ArchiveTimelineTimeAxis: Sendable, Equatable {
  public let referenceDate: Date
  public let earliestAxisDate: Date
  public let latestFutureDate: Date

  /// Earliest year plotted on the primary sqrt-recency span so extreme
  /// historical timestamps cannot compress decades of archive history into a
  /// few pixels. Older dated records still plot visibly at normalizedPosition 0.
  public static let primaryAxisMinimumYear = 1990

  public init(
    referenceDate: Date = Date(),
    earliestDatedRecordTime: Date?,
    latestFutureRecordTime: Date? = nil
  ) {
    self.referenceDate = referenceDate
    var utcCalendar = Calendar(identifier: .gregorian)
    utcCalendar.timeZone = TimeZone(secondsFromGMT: 0) ?? .gmt

    let oneYearBeforeReference =
      utcCalendar.date(byAdding: .year, value: -1, to: referenceDate)
      ?? referenceDate.addingTimeInterval(-365 * 86_400)
    let axisFloor =
      utcCalendar.date(from: DateComponents(year: Self.primaryAxisMinimumYear, month: 1, day: 1))
      ?? Date(timeIntervalSince1970: 631_152_000)

    if let earliest = earliestDatedRecordTime, earliest < oneYearBeforeReference {
      self.earliestAxisDate = max(earliest, axisFloor)
    } else {
      self.earliestAxisDate = oneYearBeforeReference
    }

    let oneYearAfterReference =
      utcCalendar.date(byAdding: .year, value: 1, to: referenceDate)
      ?? referenceDate.addingTimeInterval(365 * 86_400)
    if let latestFuture = latestFutureRecordTime, latestFuture > oneYearAfterReference {
      self.latestFutureDate = latestFuture
    } else {
      self.latestFutureDate = oneYearAfterReference
    }
  }

  public static func build(
    from histories: [ArchiveMonthlyHistory],
    referenceDate: Date = Date()
  ) -> ArchiveTimelineTimeAxis {
    var utcCalendar = Calendar(identifier: .gregorian)
    utcCalendar.timeZone = TimeZone(secondsFromGMT: 0) ?? .gmt
    let axisFloor =
      utcCalendar.date(from: DateComponents(year: Self.primaryAxisMinimumYear, month: 1, day: 1))
      ?? Date(timeIntervalSince1970: 631_152_000)

    var countsByMonth: [Date: UInt64] = [:]
    var latestFuture: Date?
    for history in histories {
      for bucket in history.monthlyRecordCountsInChronologicalOrder {
        guard bucket.recordCount > 0,
          let monthStart = Self.utcMonthStart(
            year: Int(bucket.calendarYear),
            month: Int(bucket.calendarMonth)
          )
        else {
          continue
        }
        if monthStart > referenceDate {
          if let currentLatest = latestFuture {
            if monthStart > currentLatest { latestFuture = monthStart }
          } else {
            latestFuture = monthStart
          }
        } else if monthStart >= axisFloor {
          countsByMonth[monthStart, default: 0] &+= bucket.recordCount
        }
      }
    }

    let sortedPastMonths = countsByMonth.keys.sorted()
    // Start the primary axis at the earliest month with real archive density
    // (>= 3 records) so isolated stray early timestamps do not leave a wide
    // blank margin on the left while still plotting at normalizedPosition 0.
    let earliestDenseMonth =
      sortedPastMonths.first(where: { (countsByMonth[$0] ?? 0) >= 3 })
      ?? sortedPastMonths.first

    return ArchiveTimelineTimeAxis(
      referenceDate: referenceDate,
      earliestDatedRecordTime: earliestDenseMonth,
      latestFutureRecordTime: latestFuture
    )
  }

  public var datedSpanSeconds: TimeInterval {
    max(referenceDate.timeIntervalSince(earliestAxisDate), 30 * 86_400)
  }

  public var futureSpanSeconds: TimeInterval {
    max(latestFutureDate.timeIntervalSince(referenceDate), 30 * 86_400)
  }

  public func isFuture(_ date: Date) -> Bool {
    date > referenceDate
  }

  /// Maps a date on or before `referenceDate` into [0, 1] using square-root
  /// recency so recent years breathe while decades of history stay readable.
  public func datedNormalizedPosition(for date: Date) -> Double {
    if date >= referenceDate { return 1.0 }
    let ageSeconds = referenceDate.timeIntervalSince(date)
    let ageFraction = min(max(ageSeconds / datedSpanSeconds, 0.0), 1.0)
    return 1.0 - sqrt(ageFraction)
  }

  public func date(forDatedNormalizedPosition position: Double) -> Date {
    let clamped = min(max(position, 0.0), 1.0)
    let ageFraction = (1.0 - clamped) * (1.0 - clamped)
    return referenceDate.addingTimeInterval(-ageFraction * datedSpanSeconds)
  }

  public func futureNormalizedPosition(for date: Date) -> Double {
    guard date > referenceDate else { return 0.0 }
    let aheadSeconds = date.timeIntervalSince(referenceDate)
    return min(max(aheadSeconds / futureSpanSeconds, 0.0), 1.0)
  }

  public static func inclusiveEndOfDay(
    for date: Date,
    calendar: Calendar = .current
  ) -> Date {
    let dayStart = calendar.startOfDay(for: date)
    let nextDay =
      calendar.date(byAdding: .day, value: 1, to: dayStart)
      ?? dayStart.addingTimeInterval(86_400)
    return max(dayStart, nextDay.addingTimeInterval(-0.001))
  }

  public func inclusiveCalendarDayRange(
    fromNormalizedPosition startPosition: Double,
    toNormalizedPosition endPosition: Double,
    calendar: Calendar = .current
  ) -> (earliestInclusive: Date, latestInclusive: Date) {
    let low = min(startPosition, endPosition)
    let high = max(startPosition, endPosition)
    let rawStart = date(forDatedNormalizedPosition: low)
    let rawEnd = date(forDatedNormalizedPosition: high)
    let startOfFirstDay = calendar.startOfDay(for: rawStart)
    let endOfLastDay = Self.inclusiveEndOfDay(for: rawEnd, calendar: calendar)
    return (startOfFirstDay, max(startOfFirstDay, endOfLastDay))
  }

  public func yearTicks(
    forTrackWidth trackWidth: Double,
    minimumLabelSpacing: Double = 54
  ) -> [ArchiveTimelineAxisTick] {
    guard trackWidth > 40 else { return [] }
    var utcCalendar = Calendar(identifier: .gregorian)
    utcCalendar.timeZone = TimeZone(secondsFromGMT: 0) ?? .gmt

    let startYear = utcCalendar.component(.year, from: earliestAxisDate)
    let endYear = utcCalendar.component(.year, from: referenceDate)

    var ticks: [ArchiveTimelineAxisTick] = [
      ArchiveTimelineAxisTick(id: "today", label: "Today", normalizedPosition: 1.0, isToday: true),
    ]
    var lastAcceptedPixel = trackWidth

    // Walk newest -> oldest so recent years always win priority over crowded
    // older ticks on narrow windows.
    for year in stride(from: endYear, through: startYear, by: -1) {
      guard let janFirst = Self.utcMonthStart(year: year, month: 1),
        janFirst >= earliestAxisDate,
        janFirst <= referenceDate
      else {
        continue
      }
      let pos = datedNormalizedPosition(for: janFirst)
      let pixel = pos * trackWidth
      if lastAcceptedPixel - pixel >= minimumLabelSpacing, pixel >= 18 {
        ticks.append(
          ArchiveTimelineAxisTick(
            id: "year-\(year)",
            label: String(year),
            normalizedPosition: pos,
            isToday: false
          ))
        lastAcceptedPixel = pixel
      }
    }
    return ticks.sorted { $0.normalizedPosition < $1.normalizedPosition }
  }

  public static func utcMonthStart(year: Int, month: Int) -> Date? {
    guard year > 0, (1...12).contains(month) else { return nil }
    var utcCalendar = Calendar(identifier: .gregorian)
    utcCalendar.timeZone = TimeZone(secondsFromGMT: 0) ?? .gmt
    return utcCalendar.date(from: DateComponents(year: year, month: month, day: 1))
  }

  public static func utcMonthEndExclusive(year: Int, month: Int) -> Date? {
    guard let start = utcMonthStart(year: year, month: month) else { return nil }
    var utcCalendar = Calendar(identifier: .gregorian)
    utcCalendar.timeZone = TimeZone(secondsFromGMT: 0) ?? .gmt
    return utcCalendar.date(byAdding: .month, value: 1, to: start)
  }
}

public enum ArchiveTimelineMatchRegion: Sendable, Equatable {
  case undated
  case dated
  case future
}

public struct ArchiveTimelineMatchMarkerPlacement: Sendable, Equatable {
  public let matchIdentifier: SearchMatchIdentifier
  public let region: ArchiveTimelineMatchRegion
  public let exactX: Double
  public let verticalStackOffset: Double

  public init(
    matchIdentifier: SearchMatchIdentifier,
    region: ArchiveTimelineMatchRegion,
    exactX: Double,
    verticalStackOffset: Double
  ) {
    self.matchIdentifier = matchIdentifier
    self.region = region
    self.exactX = exactX
    self.verticalStackOffset = verticalStackOffset
  }
}

public enum ArchiveTimelineMatchMarkerLayout {
  /// Places search matches at their exact horizontal date without sideways
  /// displacement. Coincident matches within `clusterThreshold` points stack
  /// vertically around the lane midline.
  public static func placements(
    for matches: [SearchMatch],
    axis: ArchiveTimelineTimeAxis,
    undatedCenterX: Double,
    datedMinX: Double,
    datedWidth: Double,
    futureMinX: Double,
    futureWidth: Double,
    clusterThreshold: Double = 4.0,
    verticalStep: Double = 3.5
  ) -> [ArchiveTimelineMatchMarkerPlacement] {
    guard !matches.isEmpty else { return [] }
    struct RawPoint {
      let id: SearchMatchIdentifier
      let region: ArchiveTimelineMatchRegion
      let x: Double
    }
    let rawPoints: [RawPoint] = matches.map { match in
      guard let time = match.time else {
        return RawPoint(id: match.id, region: .undated, x: undatedCenterX)
      }
      if axis.isFuture(time) {
        let u = axis.futureNormalizedPosition(for: time)
        return RawPoint(id: match.id, region: .future, x: futureMinX + u * futureWidth)
      }
      let u = axis.datedNormalizedPosition(for: time)
      return RawPoint(id: match.id, region: .dated, x: datedMinX + u * datedWidth)
    }

    let sorted = rawPoints.sorted { lhs, rhs in
      if lhs.x != rhs.x { return lhs.x < rhs.x }
      return lhs.id.trawlLink.globallyRoutableTrawlLink < rhs.id.trawlLink.globallyRoutableTrawlLink
    }

    var clusters: [[RawPoint]] = []
    for point in sorted {
      if var last = clusters.last,
        let anchor = last.first,
        anchor.region == point.region,
        abs(point.x - anchor.x) <= clusterThreshold
      {
        last.append(point)
        clusters[clusters.count - 1] = last
      } else {
        clusters.append([point])
      }
    }

    var result: [ArchiveTimelineMatchMarkerPlacement] = []
    result.reserveCapacity(sorted.count)
    for cluster in clusters {
      let visibleCount = min(cluster.count, 3)
      for (index, point) in cluster.enumerated() {
        let slot = index % visibleCount
        let centered = Double(slot) - Double(visibleCount - 1) / 2.0
        result.append(
          ArchiveTimelineMatchMarkerPlacement(
            matchIdentifier: point.id,
            region: point.region,
            exactX: point.x,
            verticalStackOffset: centered * verticalStep
          ))
      }
    }
    return result
  }
}

public enum ArchiveTimelineDateRangeFormatting {
  public static func label(
    earliestInclusive: Date?,
    latestInclusive: Date?,
    calendar: Calendar = .current,
    locale: Locale = .current
  ) -> String? {
    switch (earliestInclusive, latestInclusive) {
    case (nil, nil):
      return nil
    case (let start?, nil):
      return "From \(formatDay(start, includeYear: true, locale: locale))"
    case (nil, let end?):
      return "Up to \(formatDay(end, includeYear: true, locale: locale))"
    case (let start?, let end?):
      if calendar.isDate(start, inSameDayAs: end) {
        return formatDay(start, includeYear: true, locale: locale)
      }
      let sameYear = calendar.component(.year, from: start) == calendar.component(.year, from: end)
      let startText = formatDay(start, includeYear: !sameYear, locale: locale)
      let endText = formatDay(end, includeYear: true, locale: locale)
      return "\(startText) – \(endText)"
    }
  }

  private static func formatDay(_ date: Date, includeYear: Bool, locale: Locale) -> String {
    if includeYear {
      return date.formatted(.dateTime.day().month(.abbreviated).year().locale(locale))
    }
    return date.formatted(.dateTime.day().month(.abbreviated).locale(locale))
  }
}
