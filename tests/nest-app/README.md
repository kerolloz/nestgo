# nest-app fixture

A real NestJS application: decorators resolved through `@nestjs/common`, constructor
injection, and the `@nestjs/swagger` CLI plugin. It is the only fixture with a realistic
dependency graph.

`scripts/verify-nest-app.sh` builds it with nestgo and runs it, asserting:

    di=Ada schema=age,breed,name

- `di=Ada` — dependency injection resolved `CatsService` into `CatsController`, which
  only works if TypeScript 7 emitted `design:paramtypes` the way NestJS expects.
- `schema=age,breed,name` — the swagger schema is complete even though `CatDto` carries
  no `@ApiProperty` decorators, so every property came from the CLI plugin metadata that
  nestgo generated.

`src/metadata.ts` is generated during the build and is not committed.

The tsconfig is TypeScript 7 shaped: no `baseUrl`, `nodenext` resolution, explicit `.js`
extensions on relative imports. A pre-TypeScript 7 NestJS config does not compile, and
nestgo's preflight reports what to change.
