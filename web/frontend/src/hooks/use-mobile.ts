import * as React from "react"

const MOBILE_BREAKPOINT = 768
const HISTORY_SHEET_BREAKPOINT = 1024

function useMediaQuery(query: string) {
  const [isMobile, setIsMobile] = React.useState<boolean | undefined>(undefined)

  React.useEffect(() => {
    const mql = window.matchMedia(query)
    const onChange = () => {
      setIsMobile(mql.matches)
    }
    mql.addEventListener("change", onChange)
    setIsMobile(mql.matches)
    return () => mql.removeEventListener("change", onChange)
  }, [query])

  return !!isMobile
}

export function useIsMobile() {
  return useMediaQuery(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)
}

export function useIsHistorySheet() {
  return useMediaQuery(`(max-width: ${HISTORY_SHEET_BREAKPOINT - 1}px)`)
}
