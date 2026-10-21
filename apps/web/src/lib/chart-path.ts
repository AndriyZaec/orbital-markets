export interface PlotPoint {
  x: number
  y: number
}

// Fritsch-Carlson tangents keep smoothing inside each data segment, without
// the invented peaks produced by ordinary chart splines.
export function monotonePath(points: PlotPoint[]) {
  if (points.length === 0) return ''
  if (points.length === 1) return `M ${points[0].x} ${points[0].y}`

  const slopes = points.slice(1).map((point, index) => (point.y - points[index].y) / Math.max(0.0001, point.x - points[index].x))
  const tangents = points.map((_, index) => {
    if (index === 0) return slopes[0]
    if (index === points.length - 1) return slopes[slopes.length - 1]
    if (slopes[index - 1] * slopes[index] <= 0) return 0
    return (slopes[index - 1] + slopes[index]) / 2
  })

  slopes.forEach((slope, index) => {
    if (slope === 0) {
      tangents[index] = 0
      tangents[index + 1] = 0
      return
    }
    const alpha = tangents[index] / slope
    const beta = tangents[index + 1] / slope
    const magnitude = alpha * alpha + beta * beta
    if (magnitude > 9) {
      const scale = 3 / Math.sqrt(magnitude)
      tangents[index] = scale * alpha * slope
      tangents[index + 1] = scale * beta * slope
    }
  })

  return points.slice(1).reduce((path, point, index) => {
    const previous = points[index]
    const dx = point.x - previous.x
    return `${path} C ${previous.x + dx / 3} ${previous.y + tangents[index] * dx / 3}, ${point.x - dx / 3} ${point.y - tangents[index + 1] * dx / 3}, ${point.x} ${point.y}`
  }, `M ${points[0].x} ${points[0].y}`)
}
