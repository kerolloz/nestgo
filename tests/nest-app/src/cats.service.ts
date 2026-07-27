import { Injectable } from '@nestjs/common';
import { CatDto } from './cat.dto.js';

@Injectable()
export class CatsService {
  private readonly cats: CatDto[] = [{ name: 'Ada', age: 3, breed: 'tabby' }];

  findAll(): CatDto[] {
    return this.cats;
  }
}
