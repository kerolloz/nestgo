import { Controller, Get } from '@nestjs/common';
import { CatsService } from './cats.service.js';
import { CatDto } from './cat.dto.js';

@Controller('cats')
export class CatsController {
  // Constructor injection: resolved from design:paramtypes metadata.
  constructor(private readonly catsService: CatsService) {}

  @Get()
  findAll(): CatDto[] {
    return this.catsService.findAll();
  }
}
