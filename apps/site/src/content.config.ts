import { defineCollection, z } from 'astro:content';
import { glob } from 'astro/loaders';

const docs = defineCollection({
  loader: glob({ pattern: '**/*.md', base: './src/content/docs' }),
  schema: z.object({
    title: z.string(),
    description: z.string(),
    group: z.enum([
      'getting-started',
      'concepts',
      'guides',
      'reference',
      'development',
      'architecture',
      'faq',
    ]),
    order: z.number(),
    sourceDoc: z.string().optional(),
    updatedAt: z.string().optional(),
  }),
});

export const collections = { docs };
