export class Repository {
  find(): string {
    return 'row';
  }
}

export class Logger {
  log(message: string): void {
    void message;
  }
}

// Interface-typed constructor params serialize to Object in design:paramtypes,
// which is exactly why NestJS requires @Inject for them.
export interface Config {
  url: string;
}

export type Nullable<T> = T | null;
