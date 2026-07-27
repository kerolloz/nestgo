// Deliberately no @ApiProperty decorators. The swagger CLI plugin is supposed
// to infer this metadata from the types, which is the whole reason the plugin
// exists — and the reason nestgo has to generate it out-of-band.
export class CatDto {
  name!: string;
  age!: number;
  breed?: string;
}
