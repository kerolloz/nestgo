import 'reflect-metadata';
import { NestFactory } from '@nestjs/core';
import { SwaggerModule, DocumentBuilder } from '@nestjs/swagger';
import { AppModule } from './app.module.js';
// The plugin-generated metadata. nestgo writes this before compiling, so it is
// an ordinary source file by the time the compiler sees it.
import metadata from './metadata.js';
import { CatsController } from './cats.controller.js';

async function main() {
  // Without this the schema has no properties: nothing annotated the DTO by
  // hand, so everything swagger knows comes from the plugin.
  await SwaggerModule.loadPluginMetadata(metadata);

  const app = await NestFactory.create(AppModule, { logger: false });
  const document = SwaggerModule.createDocument(
    app,
    new DocumentBuilder().setTitle('cats').setVersion('1.0').build(),
  );

  // Dependency injection must have wired the service into the controller.
  const controller = app.get(CatsController);
  const names = controller.findAll().map((c) => c.name).join(',');

  const schema = (document.components?.schemas?.CatDto ?? {}) as {
    properties?: Record<string, unknown>;
  };
  const properties = Object.keys(schema.properties ?? {}).sort().join(',');

  console.log(`di=${names} schema=${properties}`);
  await app.close();
}

main();
