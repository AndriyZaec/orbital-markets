import type { ComponentProps } from 'react'
import { Loader2Icon } from 'lucide-react'
import { cn } from '@/lib/utils'

function Spinner({ className, ...props }: ComponentProps<'svg'>) {
  return (
    <Loader2Icon
      data-slot="spinner"
      role="status"
      aria-label="Loading"
      className={cn('size-4 animate-spin motion-reduce:animate-none', className)}
      {...props}
    />
  )
}

export { Spinner }
