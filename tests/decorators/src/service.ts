import { Column, Controller, Get, Inject, Injectable, Optional } from './decorators';
import { Config, Logger, Nullable, Repository } from './types';

// The shapes below are the ones NestJS dependency injection actually depends
// on. Each exercises a different design:* metadata emit path.

@Injectable()
export class UserService {
  constructor(
    // class type -> emitted as a live class reference
    private readonly repo: Repository,
    // token injection -> __param(1, Inject(...))
    @Inject('CONFIG') private readonly config: Config,
    // optional + primitive -> String
    @Optional() private readonly prefix?: string,
  ) {}

  // design:type on a property
  @Column()
  name!: string;

  // design:type / design:paramtypes / design:returntype on a method
  @Get('/users')
  findAll(limit: number, active: boolean): string[] {
    void limit;
    void active;
    return [this.repo.find()];
  }
}

@Controller('health')
export class HealthController {
  constructor(
    private readonly logger: Logger,
    // interface type -> Object
    @Inject('CONFIG') private readonly config: Config,
    // generic alias -> Object
    @Optional() private readonly last?: Nullable<Date>,
  ) {}

  @Get()
  check(): boolean {
    this.logger.log('ok');
    return true;
  }
}
