import { HealthController, UserService } from './service';
import { Logger, Repository } from './types';

const service = new UserService(new Repository(), { url: 'db://local' }, 'u');
const health = new HealthController(new Logger(), { url: 'db://local' });

console.log(`decorators ok: ${service.findAll(1, true).join(',')} ${health.check()}`);
