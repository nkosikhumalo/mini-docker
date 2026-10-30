--
-- PostgreSQL database dump
--

\restrict RoxAQ9K5arNwDqQqSYcKHEYKtG4Bp7ZSlFRDbCQrOxv55EDqaODUxwspUp7HAvC

-- Dumped from database version 16.13 (Ubuntu 16.13-0ubuntu0.24.04.1)
-- Dumped by pg_dump version 16.13 (Ubuntu 16.13-0ubuntu0.24.04.1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: interview_answers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.interview_answers (
    id bigint NOT NULL,
    session_id text,
    question_id integer NOT NULL,
    question_text text NOT NULL,
    category text DEFAULT ''::text NOT NULL,
    transcript text DEFAULT ''::text NOT NULL,
    score integer DEFAULT 0 NOT NULL,
    star text DEFAULT ''::text NOT NULL,
    summary text DEFAULT ''::text NOT NULL,
    filler_words jsonb DEFAULT '{}'::jsonb NOT NULL,
    answered_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: interview_answers_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.interview_answers_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: interview_answers_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.interview_answers_id_seq OWNED BY public.interview_answers.id;


--
-- Name: interview_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.interview_sessions (
    id text NOT NULL,
    user_id uuid,
    job_description text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    job_title text DEFAULT ''::text NOT NULL
);


--
-- Name: session_questions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.session_questions (
    id bigint NOT NULL,
    session_id text,
    question_idx integer NOT NULL,
    question_text text NOT NULL,
    category text DEFAULT ''::text NOT NULL,
    skill text DEFAULT ''::text NOT NULL
);


--
-- Name: session_questions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.session_questions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: session_questions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.session_questions_id_seq OWNED BY public.session_questions.id;


--
-- Name: user_api_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_api_keys (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    provider text NOT NULL,
    key_hint text DEFAULT ''::text NOT NULL,
    encrypted_key text NOT NULL,
    is_active boolean DEFAULT false NOT NULL,
    status text DEFAULT 'untested'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    password_hash text,
    provider text DEFAULT 'email'::text NOT NULL,
    provider_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    plan text DEFAULT 'free'::text NOT NULL,
    free_sessions_used integer DEFAULT 0 NOT NULL,
    role text DEFAULT 'user'::text NOT NULL,
    reset_token text,
    reset_token_expires_at timestamp with time zone
);


--
-- Name: interview_answers id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interview_answers ALTER COLUMN id SET DEFAULT nextval('public.interview_answers_id_seq'::regclass);


--
-- Name: session_questions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.session_questions ALTER COLUMN id SET DEFAULT nextval('public.session_questions_id_seq'::regclass);


--
-- Data for Name: interview_answers; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.interview_answers (id, session_id, question_id, question_text, category, transcript, score, star, summary, filler_words, answered_at) FROM stdin;
\.


--
-- Data for Name: interview_sessions; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.interview_sessions (id, user_id, job_description, created_at, job_title) FROM stdin;
fb40311977daa2429138c06406cb7842	6b7963d5-154a-4993-8d52-eefbc496e2c8	SQL Server & T-SQL: Strong ability to write, optimize, and troubleshoot complex SQL queries and stored procedures.\nReporting: Hands-on experience with SSRS report development and maintenance.\nC# & .NET: Practical experience with ASP.NET MVC and .NET Core, particularly in deploying and supporting applications and APIs.\nCI/CD: Azure DevOps Pipelines; experience deploying .NET and database changes.\nVersion Control: Git (Bitbucket); understanding of branching and release strategies.\nHosting & Infrastructure: IIS configuration and management.\nScripting & Automation: PowerShell experience preferred.\nOperations Mindset: Comfortable supporting production systems and diagnosing real-world issues.	2026-04-22 12:09:14.840275+02	junior Embedded Software Design and Verification Engineer
b580f9c3b9924c04d1deb34f05ce745c	b3aad908-0383-4f43-8c51-f2937095e129	We need a React developer with TypeScript, REST APIs, Jest testing, and 3+ years experience building scalable SPAs.	2026-04-22 12:14:46.926012+02	React Developer
705c882ef21d817b76f782d98103ea19	b3aad908-0383-4f43-8c51-f2937095e129	We need a React developer with TypeScript, REST APIs, Jest testing, and 3+ years experience building scalable SPAs.	2026-04-22 12:15:17.258371+02	React Developer
1432e1d6109cebf25d713f5f3db6deb8	b3aad908-0383-4f43-8c51-f2937095e129	Go developer with PostgreSQL, REST APIs, Docker, microservices, 3+ years experience.	2026-04-22 12:15:57.722542+02	Backend Engineer
a15b4d8cd0972fe1f1265471bb34d8ad	b3aad908-0383-4f43-8c51-f2937095e129	Go developer with PostgreSQL, REST APIs, Docker, microservices, 3+ years experience.	2026-04-22 12:17:45.649076+02	Backend Engineer
a3ac6496a043d453852c31b55358c954	b3aad908-0383-4f43-8c51-f2937095e129	Go developer with PostgreSQL, REST APIs, Docker, microservices, 3+ years experience.	2026-04-22 12:19:32.253196+02	Backend Engineer
d9e9735594423a8dabe7a97ac1a7296e	b3aad908-0383-4f43-8c51-f2937095e129	Go developer with PostgreSQL, REST APIs, Docker, microservices, 3+ years experience.	2026-04-22 12:20:20.755614+02	Backend Engineer
a03c57d70309b08eecd105b1c0ae1069	b3aad908-0383-4f43-8c51-f2937095e129	Go developer with PostgreSQL, REST APIs, Docker, microservices, 3+ years experience.	2026-04-22 12:21:15.20716+02	Backend Engineer
caa60287c698bcffd1dcc02f4c9328aa	6b7963d5-154a-4993-8d52-eefbc496e2c8	Junior Software Developer – Use your brilliant talents to work in a one of a kind insurance tech company!\n\nHYBRID WORKING opportunity where coding meets excellence\n\nThis is not a remote working position and standard working hours from the office in Bryanston, Johannesburg will apply. Monday’s, Wednesday’s and Friday’s are spent at the office.\n\nNo Prior Working Experience Required!\n\nWe are recruiting for an enthusiastic Junior Software Engineer to join an experienced Software Development team. You will assist with all functions of software design and development and primary focus will be to learn the codebase, gain domain knowledge, respond to requests from Senior Team members and provide support to the users.\n\nTo ensure success as a Junior Software Engineer in this opportunity, you should have a good working knowledge of basic programming languages, the ability to learn new technologies quickly and the ability to work in a team environment. Ultimately a top-class Junior Software Engineer provides valuable support to their team while continually improving their coding and development skills.\n\nDuties & Responsibilities\n\nTech Stack:\n• C# - Data Structures & Algorithms\n• SQL\n• LINQ\n• HTML & CSS\n• Javascript / Typescript\n• Angular / Blazor\n• Net Web API\n• C#\n• ASP.Net MVC\n• Javascript\n• Entity Framework\n• SSAS / SSIS / SSRS\n• Relational Database Design\n• SQL and TSQL Scripting\n• MS Net Framework\n\nResponsibilities:\n• Assisting with all aspects of software design and coding\n• Learning the codebase and improving your coding skills\n• Gaining knowledge and understanding of the full development life cycle\n• Writing and maintaining code\n• Working on minor bug fixes\n• Monitoring the technical performance of systems\n• Responding to online requests\n• Gathering information from clients about program functionality\n• Writing reports\n• Conducting development tests\n\nDesired Experience & Qualification\n\nAssessment: Online Coding Challenge to be completed with initial application\n\nRequirements:\n• Completed Matric with a minimum 60% achievement for Mathematics – Not Negotiable\n• Completed relevant IT Degree (BSc Computer Science or BCom Informatics) – Not Negotiable\n• Completed Honours Degree - highly beneficial\n• Aspirations to become a tech-savvy Software Developer – essential\n• Basic programming experience (university level)\n• Knowledge of databases and operating systems\n• Ability to learn new software and technologies quickly\n• Ability to apply logic, solve problems and work effectively in a team environment\n\nPackage & Remuneration\n\nThe Position: We’re looking for a tech-savvy professional curios about new technologies and aspiring to deliver technology that is essential to any business with an insurance function. The pay range on offer is R25 000.00 to R27 500.00 CtC Package Per Month, based on skills, tech stack and qualifications.\n\nInterested?\n\nHow to Apply:\n\nFor your application to be considered, please email your CV, Matric Certificate and Academic Transcript to cv@placed.biz – only candidates with suitable Software Development knowledge will be contacted.\n\nNote:\n• Online Coding Challenge to be completed with initial application\n• Applicants must be South African with a valid South African ID\n\nWhy you should apply:\n• Hybrid working model\n• No dress code… more time in Pj’s for remote days\n• Dynamic and enriching working environment\n• Cutting-edge Insurance Tech industry\n• Working alongside qualified industry professionals\n\nITC, ID, Criminal and Qualification checks will be done on the successful candidate.\n\nWe will reply on applications that get shortlisted only. Therefore, please deem your application as unsuccessful if you have not received feedback after 7 days.	2026-04-29 13:42:31.115285+02	Junior Software Engineer
91ce513457554edc3011f7dd6f75612e	6b7963d5-154a-4993-8d52-eefbc496e2c8	Junior Software Developer – Use your brilliant talents to work in a one of a kind insurance tech company!\n\nHYBRID WORKING opportunity where coding meets excellence\n\nThis is not a remote working position and standard working hours from the office in Bryanston, Johannesburg will apply. Monday’s, Wednesday’s and Friday’s are spent at the office.\n\nNo Prior Working Experience Required!\n\nWe are recruiting for an enthusiastic Junior Software Engineer to join an experienced Software Development team. You will assist with all functions of software design and development and primary focus will be to learn the codebase, gain domain knowledge, respond to requests from Senior Team members and provide support to the users.\n\nTo ensure success as a Junior Software Engineer in this opportunity, you should have a good working knowledge of basic programming languages, the ability to learn new technologies quickly and the ability to work in a team environment. Ultimately a top-class Junior Software Engineer provides valuable support to their team while continually improving their coding and development skills.\n\nDuties & Responsibilities\n\nTech Stack:\n• C# - Data Structures & Algorithms\n• SQL\n• LINQ\n• HTML & CSS\n• Javascript / Typescript\n• Angular / Blazor\n• Net Web API\n• C#\n• ASP.Net MVC\n• Javascript\n• Entity Framework\n• SSAS / SSIS / SSRS\n• Relational Database Design\n• SQL and TSQL Scripting\n• MS Net Framework\n\nResponsibilities:\n• Assisting with all aspects of software design and coding\n• Learning the codebase and improving your coding skills\n• Gaining knowledge and understanding of the full development life cycle\n• Writing and maintaining code\n• Working on minor bug fixes\n• Monitoring the technical performance of systems\n• Responding to online requests\n• Gathering information from clients about program functionality\n• Writing reports\n• Conducting development tests\n\nDesired Experience & Qualification\n\nAssessment: Online Coding Challenge to be completed with initial application\n\nRequirements:\n• Completed Matric with a minimum 60% achievement for Mathematics – Not Negotiable\n• Completed relevant IT Degree (BSc Computer Science or BCom Informatics) – Not Negotiable\n• Completed Honours Degree - highly beneficial\n• Aspirations to become a tech-savvy Software Developer – essential\n• Basic programming experience (university level)\n• Knowledge of databases and operating systems\n• Ability to learn new software and technologies quickly\n• Ability to apply logic, solve problems and work effectively in a team environment\n\nPackage & Remuneration\n\nThe Position: We’re looking for a tech-savvy professional curios about new technologies and aspiring to deliver technology that is essential to any business with an insurance function. The pay range on offer is R25 000.00 to R27 500.00 CtC Package Per Month, based on skills, tech stack and qualifications.\n\nInterested?\n\nHow to Apply:\n\nFor your application to be considered, please email your CV, Matric Certificate and Academic Transcript to cv@placed.biz – only candidates with suitable Software Development knowledge will be contacted.\n\nNote:\n• Online Coding Challenge to be completed with initial application\n• Applicants must be South African with a valid South African ID\n\nWhy you should apply:\n• Hybrid working model\n• No dress code… more time in Pj’s for remote days\n• Dynamic and enriching working environment\n• Cutting-edge Insurance Tech industry\n• Working alongside qualified industry professionals\n\nITC, ID, Criminal and Qualification checks will be done on the successful candidate.\n\nWe will reply on applications that get shortlisted only. Therefore, please deem your application as unsuccessful if you have not received feedback after 7 days.	2026-04-29 14:52:39.509693+02	Junior Software Engineer
\.


--
-- Data for Name: session_questions; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.session_questions (id, session_id, question_idx, question_text, category, skill) FROM stdin;
8	fb40311977daa2429138c06406cb7842	0	You are tasked with optimizing a legacy SSRS report that is timing out. Walk me through your process for using SQL Server Execution Plans to identify and resolve bottlenecks in the underlying T-SQL stored procedure.	Technical	SQL Optimization & SSRS
9	fb40311977daa2429138c06406cb7842	1	Describe how you would configure an Azure DevOps Pipeline to automate the deployment of a .NET Core API to an IIS server, specifically highlighting how you manage environment-specific configurations using PowerShell.	Technical	CI/CD & Infrastructure
10	fb40311977daa2429138c06406cb7842	2	Tell me about a time you discovered a critical bug in a production environment. How did you use logs or diagnostic tools to identify the root cause, and how did you manage the hotfix process using your Git branching strategy?	Behavioral	Operations & Troubleshooting
11	fb40311977daa2429138c06406cb7842	3	When building an ASP.NET MVC application that interacts with a SQL database, what architectural patterns do you follow to ensure your T-SQL queries are both performant and secure against injection attacks?	Technical	C# & .NET Security
12	fb40311977daa2429138c06406cb7842	4	Describe a situation where you had to balance a rapid deployment request with the need for thorough verification. How did you ensure the quality of the release while meeting the operational deadline?	Behavioral	Release Management
13	705c882ef21d817b76f782d98103ea19	0	How do you leverage TypeScript's advanced features, such as Generics or Discriminated Unions, to ensure type safety when handling diverse data structures from a REST API?	Technical	TypeScript
14	705c882ef21d817b76f782d98103ea19	1	Describe your strategy for writing maintainable tests with Jest. How do you decide between unit testing individual hooks and integration testing full component trees?	Technical	Jest testing
15	705c882ef21d817b76f782d98103ea19	2	Tell me about a specific time you identified a performance bottleneck in a large-scale React SPA. What steps did you take to diagnose the issue and how did you resolve it?	Behavioral	Problem Solving
16	705c882ef21d817b76f782d98103ea19	3	When designing a scalable SPA architecture, how do you organize your folder structure and state management to ensure the project remains maintainable as it grows over 3+ years?	Technical	System Design
17	705c882ef21d817b76f782d98103ea19	4	Describe a situation where you had to advocate for a specific technical approach (like a library choice or architectural pattern) to your team. How did you communicate the benefits and handle any pushback?	Behavioral	Communication
18	1432e1d6109cebf25d713f5f3db6deb8	0	Explain how you manage concurrency in Go using goroutines and channels. Can you describe a scenario where you had to debug a race condition or a deadlock in a production microservice?	Technical	Go Concurrency
19	1432e1d6109cebf25d713f5f3db6deb8	1	Describe your process for optimizing a slow PostgreSQL query. How do you utilize tools like EXPLAIN ANALYZE, and what strategies do you use for indexing or schema refactoring in a high-traffic environment?	Technical	PostgreSQL Optimization
20	1432e1d6109cebf25d713f5f3db6deb8	2	When designing a REST API for a microservice, how do you handle versioning, error handling, and ensuring the service remains scalable and containerized with Docker?	Technical	System Design
21	1432e1d6109cebf25d713f5f3db6deb8	3	Tell me about a time you had to lead a technical initiative or mentor a junior developer. How did you ensure the project met its deadlines while maintaining high code quality?	Behavioral	Leadership
22	1432e1d6109cebf25d713f5f3db6deb8	4	Describe a situation where you disagreed with a peer's architectural choice for a backend service. How did you communicate your perspective and reach a resolution that benefited the project?	Behavioral	Communication
23	a03c57d70309b08eecd105b1c0ae1069	0	Explain how you would implement a worker pool pattern in Go using goroutines and channels. How do you ensure that the application handles graceful shutdowns without losing in-flight data?	Technical	Go Concurrency
24	a03c57d70309b08eecd105b1c0ae1069	1	Describe your process for identifying and optimizing a slow-performing PostgreSQL query in a production environment. When would you choose a GIN index over a B-tree index?	Technical	PostgreSQL
25	a03c57d70309b08eecd105b1c0ae1069	2	In a microservices architecture, how do you manage data consistency across services when a REST API call triggers updates in multiple databases? Explain your experience with patterns like Saga or Two-Phase Commit.	Technical	System Design
26	a03c57d70309b08eecd105b1c0ae1069	3	Tell me about a time you encountered a significant performance bottleneck in a Dockerized Go application. How did you profile the service, and what steps did you take to resolve the issue?	Behavioral	Problem Solving
27	a03c57d70309b08eecd105b1c0ae1069	4	Describe a situation where you had to advocate for a specific backend architectural change (e.g., moving to a new library or refactoring a service). How did you communicate the technical debt and benefits to your team?	Behavioral	Communication
28	caa60287c698bcffd1dcc02f4c9328aa	0	As this role emphasizes learning our codebase and the insurance domain, can you describe a time you had to learn a complex new technology or subject quickly? What steps did you take to master it?	Behavioral	Learning Agility
29	caa60287c698bcffd1dcc02f4c9328aa	1	In an insurance context, how would you design a basic class structure or data model to represent a 'Policy' and its relationship to a 'Client'?	Technical	Data Modeling
30	caa60287c698bcffd1dcc02f4c9328aa	2	A user reports that they are unable to download their policy document from the portal. Walk me through your logical process for troubleshooting and identifying where the bug might be.	Technical	Problem Solving
31	caa60287c698bcffd1dcc02f4c9328aa	3	Since this role involves supporting senior team members and users, how do you handle receiving critical feedback on your code or being asked to pivot to a high-priority support task?	Behavioral	Communication
32	caa60287c698bcffd1dcc02f4c9328aa	4	Explain the difference between a GET and a POST request, and provide an example of when we would use each within an insurance application (e.g., viewing a quote vs. submitting a claim).	Technical	Web Development Fundamentals
33	91ce513457554edc3011f7dd6f75612e	0	As this is an entry-level role, you will need to absorb a lot of information quickly. Can you describe a time you had to learn a complex new technology or domain from scratch? What was your process?	Behavioral	Adaptability
34	91ce513457554edc3011f7dd6f75612e	1	A user reports that a specific insurance premium calculation is returning an incorrect value. Walk us through the logical steps you would take to debug the code and identify where the error lies.	Technical	Problem Solving
35	91ce513457554edc3011f7dd6f75612e	2	This role follows a hybrid model in Bryanston. How do you ensure effective communication and progress updates with your senior team members when you are working remotely versus in the office?	Behavioral	Communication
36	91ce513457554edc3011f7dd6f75612e	3	Explain the concept of Object-Oriented Programming (OOP) and give an example of how you might use 'Inheritance' when designing a system that handles different types of insurance policies (e.g., Life vs. Car insurance).	Technical	Coding
37	91ce513457554edc3011f7dd6f75612e	4	In a fast-paced InsurTech environment, requirements can change quickly. How do you handle receiving critical feedback on your code from a senior developer, and how do you prioritize your learning to address those gaps?	Behavioral	Professional Development
\.


--
-- Data for Name: user_api_keys; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.user_api_keys (id, user_id, provider, key_hint, encrypted_key, is_active, status, created_at, updated_at) FROM stdin;
\.


--
-- Data for Name: users; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.users (id, email, name, password_hash, provider, provider_id, created_at, updated_at, plan, free_sessions_used, role, reset_token, reset_token_expires_at) FROM stdin;
b3aad908-0383-4f43-8c51-f2937095e129	test@interviewdojo.com	Test User	$2a$10$ZpeLg9BhV3hPEngFKGwXZ.hg8E4ry13Ih.H9NYZhMstyN2NUVh.XS	email	\N	2026-04-21 13:08:43.739067+02	2026-04-21 13:08:43.739067+02	free	0	user	\N	\N
c56e975d-28a4-4ce7-96fd-b4478adf3b33	quota@test.com	Quota Test	$2a$10$npXyAlF9i2BiWn2KzwF8Ke8S4XvM40NiRjP8AFZooUgPFCtDLSUta	email	\N	2026-04-22 14:03:08.554669+02	2026-04-22 14:03:08.554669+02	free	0	user	\N	\N
fa9e13e8-801c-401c-a2fd-987b8f6c04f3	quota2@test.com	Quota Test	$2a$10$/GAZWzA66UkBDlDy9zWH../F1iZPXcQi4k/osWEEUAO8OLiKyC9Ue	email	\N	2026-04-22 14:03:35.958953+02	2026-04-22 14:03:35.958953+02	free	0	user	\N	\N
7ac59d1c-7ea7-4237-8a0b-96fd00803f76	quota3@test.com	Quota Test	$2a$10$A/TndRogSEWwgn4PWKDd8u8feTTDDC0UbeGqRaTH6yuwTw8rfror6	email	\N	2026-04-22 14:04:19.518646+02	2026-04-22 14:04:19.518646+02	free	0	user	\N	\N
c4e36712-6c86-4dfc-8b4d-c82a647f1ef2	test2@test.com	Test	$2a$10$ZzQAIuYXF2g799mW.DeTgeTKn5jq/WzDWkioOzK7tAu2JzKtu9Wmu	email	\N	2026-04-22 14:19:18.051464+02	2026-04-22 14:19:18.051464+02	free	0	user	\N	\N
da3bf9f6-7706-4ed2-a35f-df6c56512e9f	newuser@test.com	New User	$2a$10$CskUjYTt4c3/zNkPObd3gOUB8DijZuxpC1WgBBgwyWrsEmBEYLiCq	email	\N	2026-04-22 14:35:55.300853+02	2026-04-22 14:35:55.300853+02	free	0	user	\N	\N
6b7963d5-154a-4993-8d52-eefbc496e2c8	babongilenkosimphile101@gmail.com	Babongile Nkosimphile	$2a$10$D3A8o2eoFpnoE0yT8XvhB.GxB2gvGKYlFlOZTrE1le2pN0UtZV26y	email	\N	2026-04-21 13:10:12.07191+02	2026-04-29 14:33:23.651747+02	enterprise	0	admin	\N	\N
a6baa579-0ca2-4168-a1fc-fbb809b7bb27	nkosi_10@outlook.com	Mbali 	$2a$10$h8k4lO0sumysT4eQZYwNC.y.8f56MK4QpWCcutiE20N2Zv3BZmKPa	email	\N	2026-04-22 14:43:09.935736+02	2026-04-29 14:38:37.260314+02	free	0	user	\N	\N
\.


--
-- Name: interview_answers_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.interview_answers_id_seq', 1, false);


--
-- Name: session_questions_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.session_questions_id_seq', 37, true);


--
-- Name: interview_answers interview_answers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interview_answers
    ADD CONSTRAINT interview_answers_pkey PRIMARY KEY (id);


--
-- Name: interview_sessions interview_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interview_sessions
    ADD CONSTRAINT interview_sessions_pkey PRIMARY KEY (id);


--
-- Name: session_questions session_questions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.session_questions
    ADD CONSTRAINT session_questions_pkey PRIMARY KEY (id);


--
-- Name: user_api_keys user_api_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_api_keys
    ADD CONSTRAINT user_api_keys_pkey PRIMARY KEY (id);


--
-- Name: user_api_keys user_api_keys_user_id_provider_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_api_keys
    ADD CONSTRAINT user_api_keys_user_id_provider_key UNIQUE (user_id, provider);


--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: idx_answers_session; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_answers_session ON public.interview_answers USING btree (session_id);


--
-- Name: idx_apikeys_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_apikeys_user ON public.user_api_keys USING btree (user_id);


--
-- Name: idx_questions_session; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_questions_session ON public.session_questions USING btree (session_id);


--
-- Name: idx_sessions_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sessions_user ON public.interview_sessions USING btree (user_id);


--
-- Name: idx_users_email; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_users_email ON public.users USING btree (email);


--
-- Name: interview_answers interview_answers_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interview_answers
    ADD CONSTRAINT interview_answers_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.interview_sessions(id) ON DELETE CASCADE;


--
-- Name: interview_sessions interview_sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interview_sessions
    ADD CONSTRAINT interview_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: session_questions session_questions_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.session_questions
    ADD CONSTRAINT session_questions_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.interview_sessions(id) ON DELETE CASCADE;


--
-- Name: user_api_keys user_api_keys_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_api_keys
    ADD CONSTRAINT user_api_keys_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

\unrestrict RoxAQ9K5arNwDqQqSYcKHEYKtG4Bp7ZSlFRDbCQrOxv55EDqaODUxwspUp7HAvC

