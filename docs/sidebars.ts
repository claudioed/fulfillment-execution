import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';
import restApiSidebar from './docs/api-reference/rest/sidebar';

/**
 * The six top-level categories are shared by every warehouse-systems docs
 * site so the five services read as one family. Order matters and is fixed:
 * Overview -> Business Context -> Domain-Driven Design -> API Reference ->
 * Ecosystem -> Architecture Decision Records.
 */
const sidebars: SidebarsConfig = {
  docsSidebar: [
    {
      type: 'category',
      label: 'Overview',
      collapsed: false,
      link: {type: 'doc', id: 'overview/index'},
      items: [
        'overview/task-lifecycle',
        'overview/architecture',
        'overview/running-locally',
      ],
    },
    {
      type: 'category',
      label: 'Business Context',
      collapsed: false,
      items: [
        'business-context/domain-vision',
        'business-context/process-paths',
        'business-context/why-pull-not-push',
        'business-context/ubiquitous-language',
      ],
    },
    {
      type: 'category',
      label: 'Domain-Driven Design',
      collapsed: false,
      items: [
        'ddd/subdomain-classification',
        'ddd/aggregates-and-invariants',
        'ddd/domain-events',
        'ddd/use-cases',
        'ddd/context-relationships',
      ],
    },
    {
      type: 'category',
      label: 'API Reference',
      collapsed: false,
      link: {type: 'doc', id: 'api-reference/index'},
      items: [
        {
          type: 'category',
          label: 'REST API (generated from apis/openapi.yaml)',
          items: restApiSidebar,
        },
        'api-reference/events',
      ],
    },
    {
      type: 'category',
      label: 'Ecosystem',
      collapsed: false,
      items: ['ecosystem/context-map', 'ecosystem/integration-contracts'],
    },
    {
      type: 'category',
      label: 'AI Ecosystem (MCP)',
      collapsed: false,
      items: ['mcp/governance-charter'],
    },
    {
      type: 'category',
      label: 'Architecture Decision Records (ADR)',
      collapsed: false,
      link: {type: 'doc', id: 'adr/index'},
      items: [
        'adr/0001-hexagonal-ports-and-adapters',
        'adr/0002-pull-based-claimnext-dispatch',
        'adr/0003-lease-based-at-most-once-claiming',
        'adr/0004-kafka-integration-events-and-envelope',
        'adr/0005-rfc-7807-problem-details',
        'adr/0006-arch-go-architecture-fitness-tests',
        'adr/0007-godog-bdd-acceptance-tests',
        'adr/0008-mcp-inbound-adapter',
        'adr/0009-fragile-and-hazmat-handling-flags',
        'adr/0010-package-segregation-and-sort-lane',
      ],
    },
  ],
};

export default sidebars;
