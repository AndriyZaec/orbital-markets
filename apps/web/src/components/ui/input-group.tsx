import type { ComponentProps } from "react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

function InputGroup({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      data-slot="input-group"
      role="group"
      className={cn(
        "group/input-group relative flex h-8 w-full min-w-0 items-center rounded-lg border border-input bg-input/30 transition-colors outline-none has-[[data-slot=input-group-control]:focus-visible]:border-ring has-[[data-slot=input-group-control]:focus-visible]:ring-3 has-[[data-slot=input-group-control]:focus-visible]:ring-ring/50",
        className
      )}
      {...props}
    />
  )
}

function InputGroupAddon({
  align = "inline-start",
  className,
  ...props
}: ComponentProps<"div"> & { align?: "inline-start" | "inline-end" }) {
  return (
    <div
      role="group"
      data-slot="input-group-addon"
      data-align={align}
      className={cn(
        "flex items-center justify-center text-muted-foreground [&>svg:not([class*='size-'])]:size-4",
        align === "inline-start" ? "order-first pl-2" : "order-last pr-1",
        className
      )}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("button")) return
        event.currentTarget.parentElement?.querySelector("input")?.focus()
      }}
      {...props}
    />
  )
}

function InputGroupButton({
  className,
  ...props
}: Omit<ComponentProps<typeof Button>, "size">) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-xs"
      className={cn("shadow-none", className)}
      {...props}
    />
  )
}

function InputGroupInput({ className, ...props }: ComponentProps<"input">) {
  return (
    <Input
      data-slot="input-group-control"
      className={cn(
        "flex-1 rounded-none border-0 bg-transparent shadow-none ring-0 focus-visible:ring-0 dark:bg-transparent",
        className
      )}
      {...props}
    />
  )
}

export { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput }
