going to create RAG+temporal RAG search and indexing files application written on go with postgresql server.
use embedding ollama container to get embedding for search and file chunk processing
use gorm as ORM
Models
  Document
  Chunk
fields for models described in migration.sql.
steps 
3 Background process in goroutine. Process documents from db with temporal path and no processing_error=nil
3.1 read file by chunks and write them into chunks table (use gorm as ORM) they should be linked with document record.
amount of chunk should be optimal for postgresql search speed and human ux.
3.2 remove temporal_path in case of success, fill processing_error field by error in case of any errors
