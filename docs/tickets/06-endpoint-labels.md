# 06 Remote Endpoint labelling
Status: closed
Blocked by: 05

Spec user story 48. Implementation Decisions › Remote Endpoint labelling.

- Reverse DNS via the Resolver port, cached with a TTL and looked up asynchronously, so an API call never blocks on DNS.
- Organisation from an offline IP→ASN/organisation database in the data dir: the iptoasn.com `ip2asn-combined.tsv.gz` format (PDDL licence). Handle the file being missing, and reload it when it changes.
- The Device detail endpoints carry `host` and `org`.
