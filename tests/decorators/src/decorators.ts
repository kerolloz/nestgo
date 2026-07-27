// Minimal stand-ins for the NestJS decorators that matter for dependency
// injection. The fixture deliberately has no npm dependencies: what is being
// checked is the compiler's __decorate/__param/__metadata output, not NestJS.

export function Injectable(): ClassDecorator {
  return () => {};
}

export function Controller(prefix?: string): ClassDecorator {
  return () => {};
}

export function Inject(token: string): ParameterDecorator {
  return () => {};
}

export function Optional(): ParameterDecorator {
  return () => {};
}

export function Get(path?: string): MethodDecorator {
  return () => {};
}

export function Column(): PropertyDecorator {
  return () => {};
}
