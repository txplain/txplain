import { useState } from 'react'
import type { Annotation } from '../types'

interface AnnotatedTextProps {
  text: string
  annotations: Annotation[]
  className?: string
}

interface AnnotatedSegment {
  text: string
  annotation?: Annotation
  index: number // For handling duplicates like "0@USDT", "1@USDT"
}

const AnnotatedText = ({ text, annotations, className = '' }: AnnotatedTextProps) => {
  const [hoveredSegment, setHoveredSegment] = useState<number | null>(null)

  // Parse the text and apply annotations
  const parseText = (): AnnotatedSegment[] => {
    if (!annotations || annotations.length === 0) {
      return [{ text, annotation: undefined, index: 0 }]
    }

    // Create a simplified approach for text parsing
    const segments: AnnotatedSegment[] = []
    let segmentIndex = 0

    // Process annotations by finding exact text matches
    // Sort by text length (longest first) to handle overlapping matches
    const sortedAnnotations = [...annotations].sort((a, b) => {
      const textA = a.text.includes('@') ? a.text.split('@')[1] : a.text
      const textB = b.text.includes('@') ? b.text.split('@')[1] : b.text
      return textB.length - textA.length
    })

    // Keep track of which parts of the text have been processed
    const processedRanges: Array<{start: number, end: number, annotation: Annotation}> = []

    // Find all annotation matches first
    for (const annotation of sortedAnnotations) {
      let searchText = annotation.text
      let targetIndex = 0

      // Handle indexed annotations like "0@PEPE"
      if (searchText.includes('@')) {
        const parts = searchText.split('@')
        if (parts.length === 2 && !isNaN(parseInt(parts[0]))) {
          targetIndex = parseInt(parts[0])
          searchText = parts[1]
        }
      }

      // Find all occurrences of this text
      let currentIndex = -1
      let occurrenceCount = 0
      
      while ((currentIndex = text.indexOf(searchText, currentIndex + 1)) !== -1) {
        // Check if this occurrence conflicts with already processed ranges
        const conflictsWithExisting = processedRanges.some(range => 
          (currentIndex >= range.start && currentIndex < range.end) ||
          (currentIndex + searchText.length > range.start && currentIndex < range.end)
        )

        if (!conflictsWithExisting) {
          // Check if this is the target occurrence index
          if (occurrenceCount === targetIndex) {
            processedRanges.push({
              start: currentIndex,
              end: currentIndex + searchText.length,
              annotation
            })
            break
          }
          occurrenceCount++
        }
      }
    }

    // Sort ranges by start position
    processedRanges.sort((a, b) => a.start - b.start)

    // Build segments from processed ranges
    let currentPos = 0
    
    for (const range of processedRanges) {
      // Add text before this annotation
      if (range.start > currentPos) {
        segments.push({
          text: text.substring(currentPos, range.start),
          annotation: undefined,
          index: segmentIndex++
        })
      }

      // Add the annotated text
      segments.push({
        text: text.substring(range.start, range.end),
        annotation: range.annotation,
        index: segmentIndex++
      })

      currentPos = range.end
    }

    // Add any remaining text
    if (currentPos < text.length) {
      segments.push({
        text: text.substring(currentPos),
        annotation: undefined,
        index: segmentIndex++
      })
    }

    // If no annotations were processed, return the original text
    if (segments.length === 0) {
      return [{ text, annotation: undefined, index: 0 }]
    }

    return segments
  }

  const segments = parseText()

  const renderTooltip = (annotation: Annotation, segmentIndex: number) => {
    if (!annotation.tooltip || hoveredSegment !== segmentIndex) {
      return null
    }

    return (
      <div className="absolute z-50 bg-gray-900 text-white text-sm rounded-lg p-3 shadow-lg border border-gray-700 min-w-48 max-w-md w-max -mb-2 left-1/2 transform -translate-x-1/2 bottom-full pointer-events-none">
        <div className="tooltip-content" dangerouslySetInnerHTML={{ __html: annotation.tooltip }} />
        <div className="absolute top-full left-1/2 transform -translate-x-1/2 w-0 h-0 border-l-4 border-r-4 border-t-4 border-transparent border-t-gray-900"></div>
      </div>
    )
  }

  return (
    <div className={`inline ${className}`}>
      {segments.map((segment) => {
        if (!segment.annotation) {
          return <span key={segment.index}>{segment.text}</span>
        }

        const { annotation } = segment
        const hasLink = annotation.link && annotation.link.startsWith('http')
        const hasTooltip = !!annotation.tooltip
        const hasIcon = !!annotation.icon

        const content = (
          <span
            className={`relative inline-flex items-center gap-1 ${
              hasLink 
                ? 'text-blue-600 hover:text-blue-800 cursor-pointer underline decoration-dotted' 
                : hasTooltip 
                  ? 'text-gray-800 font-semibold underline decoration-dotted decoration-gray-400 hover:decoration-gray-600 cursor-help'
                  : ''
            }`}
            onMouseEnter={() => hasTooltip && setHoveredSegment(segment.index)}
            onMouseLeave={() => hasTooltip && setHoveredSegment(null)}
          >
            {hasIcon && (
              <img 
                src={annotation.icon} 
                alt="" 
                className="w-4 h-4 inline-block rounded-full"
                onError={(e) => {
                  // Hide broken images
                  const target = e.target as HTMLImageElement
                  target.style.display = 'none'
                }}
              />
            )}
            {segment.text}
            {renderTooltip(annotation, segment.index)}
          </span>
        )

        if (hasLink) {
          return (
            <a
              key={segment.index}
              href={annotation.link}
              target="_blank"
              rel="noopener noreferrer"
              className="no-underline"
            >
              {content}
            </a>
          )
        }

        return <span key={segment.index}>{content}</span>
      })}
    </div>
  )
}

export default AnnotatedText 