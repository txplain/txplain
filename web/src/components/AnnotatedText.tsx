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

    // Create a map to track text occurrences for indexing
    const textOccurrences: { [key: string]: number } = {}
    
    // Sort annotations by text length (longest first) to handle overlapping matches better
    const sortedAnnotations = [...annotations].sort((a, b) => b.text.length - a.text.length)
    
    const segments: AnnotatedSegment[] = []
    let remainingText = text

    while (remainingText.length > 0) {
      let found = false
      
      for (const annotation of sortedAnnotations) {
        let searchText = annotation.text
        let targetIndex = 0
        
        // Handle indexed annotations like "0@USDT"
        if (searchText.includes('@')) {
          const parts = searchText.split('@')
          if (parts.length === 2 && !isNaN(parseInt(parts[0]))) {
            targetIndex = parseInt(parts[0])
            searchText = parts[1]
          }
        }
        
        const matchIndex = remainingText.indexOf(searchText)
        if (matchIndex !== -1) {
          // Check if this is the correct occurrence index
          const currentOccurrence = textOccurrences[searchText] || 0
          if (currentOccurrence === targetIndex) {
            // Add text before the match as a non-annotated segment
            if (matchIndex > 0) {
              segments.push({
                text: remainingText.substring(0, matchIndex),
                annotation: undefined,
                index: segments.length
              })
            }
            
            // Add the annotated segment
            segments.push({
              text: searchText,
              annotation,
              index: segments.length
            })
            
            // Update remaining text
            remainingText = remainingText.substring(matchIndex + searchText.length)
            
            // Update occurrence count
            textOccurrences[searchText] = currentOccurrence + 1
            
            found = true
            break
          } else {
            // Increment occurrence count but don't match yet
            textOccurrences[searchText] = currentOccurrence + 1
          }
        }
      }
      
      if (!found) {
        // No annotation found, take the first character and continue
        segments.push({
          text: remainingText.charAt(0),
          annotation: undefined,
          index: segments.length
        })
        remainingText = remainingText.substring(1)
      }
    }
    
    // Merge consecutive non-annotated segments
    const mergedSegments: AnnotatedSegment[] = []
    for (const segment of segments) {
      const lastSegment = mergedSegments[mergedSegments.length - 1]
      if (lastSegment && !lastSegment.annotation && !segment.annotation) {
        lastSegment.text += segment.text
      } else {
        mergedSegments.push({ ...segment, index: mergedSegments.length })
      }
    }
    
    return mergedSegments
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